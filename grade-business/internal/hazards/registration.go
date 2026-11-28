package hazards

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/httpx"
)

const (
	ledgerVulnerability = "vulnerability"
	ledgerMajorRisk     = "major_risk"
	statusDispatch      = "pending_dispatch"
	actionDispatch      = "dispatch"
	actionSupervise     = "supervise"
)

var (
	errDuplicateRegistration = errors.New("hazard already registered")
	errRegistrationFiling    = errors.New("hazard needs an active filing and a retest institution")
	errAlreadySupervised     = errors.New("hazard supervised today")
)

type registrationInput struct {
	Ledger         string `json:"ledger"`
	FilingID       string `json:"filingId"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Source         string `json:"source"`
	DiscoveredOn   string `json:"discoveredOn"`
	Deadline       string `json:"deadline"`
	Description    string `json:"description"`
	Measure        string `json:"measure"`
	Responsibility string `json:"responsibility"`
}

type registrationFilingOption struct {
	ID               string `json:"id"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	AssessmentOrg    string `json:"assessmentOrg"`
}

const registrationEligible = `kind='filing' AND status='approved' AND cancelled_at IS NULL
	AND length(btrim(organization_name)) BETWEEN 1 AND 200
	AND length(btrim(assessment_org)) BETWEEN 1 AND 200`

// handleRegister 接收监管端台账登记，校验实际材料字段和对应的有效备案。
// 参数 w 为响应对象，r 为携带登记信息的同源请求。
// 返回值 无。
func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var input registrationInput
	if r.URL.RawQuery != "" || httpx.Decode(w, r, &input) != nil || validateRegistration(&input) != nil {
		respondError(w, errInvalid)
		return
	}
	result, err := s.registerHazard(r.Context(), input)
	respond(w, http.StatusCreated, result, err)
}

// validateRegistration 规范化标题、校验自由文本及业务枚举和日期顺序。
// 参数 input 为待校验的登记数据，标题和说明可消除无意义空格。
// 返回值 参数符合登记口径时为 nil。
func validateRegistration(input *registrationInput) error {
	input.Title = strings.Join(strings.Fields(input.Title), " ")
	input.Description = strings.TrimSpace(input.Description)
	input.Measure = strings.TrimSpace(input.Measure)
	input.Responsibility = strings.TrimSpace(input.Responsibility)
	if !validID(input.FilingID) || !validLedger(input.Ledger) || input.Ledger == "" ||
		!validSeverity(input.Severity) || input.Severity == "" ||
		!validRegistrationSource(input.Source) {
		return errInvalid
	}
	for _, field := range []struct {
		value string
		max   int
	}{{input.Title, 160}, {input.Description, 300}, {input.Measure, 600}, {input.Responsibility, 160}} {
		if field.value == "" || !validText(field.value, field.max) {
			return errInvalid
		}
	}
	discovery, err := time.Parse(time.DateOnly, input.DiscoveredOn)
	deadline, dueErr := time.Parse(time.DateOnly, input.Deadline)
	if err != nil || dueErr != nil || discovery.Year() < 1 || deadline.Before(discovery) {
		return errInvalid
	}
	return nil
}

// validRegistrationSource 检查登记来源是否属于当前台账定义。
// 参数 source 为监管选中的发现来源。
// 返回值 来源受支持时为真。
func validRegistrationSource(source string) bool {
	return source == "manual" || source == "assessment" || source == "scan"
}

// registerHazard 在事务内校验备案及业务日期，写入唯一待派单事项和登记留痕。
// 参数 ctx 控制事务生命周期，input 为已核对登记参数。
// 返回值 实际新增台账版本，或备案、重复登记及落库错误。
func (s *Service) registerHazard(ctx context.Context, input registrationInput) (hazardMutation, error) {
	var result hazardMutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		_, today, err := lockRegistrationFiling(ctx, tx, input.FilingID)
		if err != nil {
			return err
		}
		if input.DiscoveredOn > today {
			return errInvalid
		}
		result, err = createRegistration(ctx, tx, input)
		if err != nil {
			return err
		}
		operator, actor := hazardAuditActor(ctx, s.operator)
		_, err = tx.Exec(ctx, `INSERT INTO hazard_events
			(hazard_id,action,from_status,to_status,operator,version,operator_id,operator_role)
			VALUES($1,'register','','pending_dispatch',$2,1,NULLIF($3,'')::uuid,NULLIF($4,''))`,
			result.ID, operator, actor.ID, actor.Role)
		return err
	})
	return result, err
}

// createRegistration 写入一条直接登记记录，利用唯一索引拦截同时登记的相同问题。
// 参数 ctx 控制写入，tx 为已锁备案的事务，input 是所需业务字段。
// 返回值 台账标识、待派单状态和版本，或重复登记与数据库错误。
func createRegistration(ctx context.Context, tx pgx.Tx, input registrationInput) (hazardMutation, error) {
	var result hazardMutation
	err := tx.QueryRow(ctx, `INSERT INTO hazard_items
		(registration_filing_id,ledger,title,severity,source,discovered_on,
		 deadline,description,measure,responsibility,status)
		VALUES($1,$2,$3,$4,$5,$6::text::date,$7::text::date,$8,$9,$10,'pending_dispatch')
		ON CONFLICT DO NOTHING RETURNING id::text,status,version`,
		input.FilingID, input.Ledger, input.Title, input.Severity, input.Source,
		input.DiscoveredOn, input.Deadline, input.Description, input.Measure, input.Responsibility).
		Scan(&result.ID, &result.Status, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = errDuplicateRegistration
	}
	return result, err
}

// lockRegistrationFiling 获取有效备案的登记机构与上海当前日期，并稳定备案归属。
// 参数 ctx 控制查询，tx 为登记或派单事务，filingID 为系统备案标识。
// 返回值 登记机构、业务日期，或备案状态冲突及数据库错误。
func lockRegistrationFiling(ctx context.Context, tx pgx.Tx, filingID string) (string, string, error) {
	var institution, today string
	districts := access.Districts(ctx)
	err := tx.QueryRow(ctx, `SELECT assessment_org,to_char(
		(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date,'YYYY-MM-DD')
		FROM business_cases f WHERE f.id=$1 AND `+registrationEligible+" AND "+
		access.FilingDistrictScope("f.id", "$2", true)+" FOR SHARE OF f", filingID, districts).
		Scan(&institution, &today)
	if errors.Is(err, pgx.ErrNoRows) {
		if districts != nil {
			return "", "", errNotFound
		}
		err = errRegistrationFiling
	}
	return institution, today, err
}

// handleFilingOptions 返回符合直接登记条件的系统备案与实际业务归属。
// 参数 w 为响应对象，r 具有可选一次系统、单位或编号关键字。
// 返回值 无。
func (s *Service) handleFilingOptions(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	value := strings.TrimSpace(query.Get("q"))
	if err != nil || len(query) > 1 || (len(query) == 1 && len(query["q"]) != 1) || !validText(value, 120) {
		respondError(w, errInvalid)
		return
	}
	result, err := s.registrationOptions(r.Context(), value)
	respond(w, http.StatusOK, hazardCollection[registrationFilingOption]{Items: result}, err)
}

// registrationOptions 用稳定排序查询可用于直接登记的备案，结果最多三十条。
// 参数 ctx 控制查询，keyword 为系统、单位或编号片段。
// 返回值 最小备案选择项或数据库错误。
func (s *Service) registrationOptions(ctx context.Context, keyword string) ([]registrationFilingOption, error) {
	rows, err := s.db.Query(ctx, `SELECT id::text,number,system_name,organization_name,assessment_org
		FROM business_cases f WHERE `+registrationEligible+`
		AND ($1='' OR strpos(lower(concat_ws(' ',number,system_name,organization_name)),lower($1))>0)
		AND `+access.FilingDistrictScope("f.id", "$2", false)+`
		ORDER BY created_at DESC,id LIMIT 30`, keyword, access.Districts(ctx))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[registrationFilingOption])
}
