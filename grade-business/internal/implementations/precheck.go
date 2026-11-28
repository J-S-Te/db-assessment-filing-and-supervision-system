package implementations

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/access"
)

type materialPrecheck struct {
	ReportVersion    int64             `json:"reportVersion"`
	FilingVersion    *int64            `json:"filingVersion,omitempty"`
	MaterialRevision *int64            `json:"materialRevision,omitempty"`
	Findings         []precheckFinding `json:"findings"`
}

type precheckFinding struct {
	RuleID   string            `json:"ruleId"`
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	Evidence *precheckEvidence `json:"evidence,omitempty"`
}

type precheckEvidence struct {
	AttachmentID string `json:"attachmentId"`
	FileName     string `json:"fileName"`
}

type precheckFiling struct {
	choice
	Version          int64
	MaterialRevision int64
	Active           bool
}

// precheckHTTP 接收仅含已查看报备版本的辅助预检请求。
// 参数 w、r 为响应及请求，institution 为已验证的机构范围，其他入口留空。
// 返回值 无。
func (s *Service) precheckHTTP(w http.ResponseWriter, r *http.Request, institution string) {
	id, ok := requestID(w, r)
	if !ok {
		return
	}
	values, err := filterValues(r.URL.RawQuery, "expectedVersion")
	version, versionErr := strconv.ParseInt(values.Get("expectedVersion"), 10, 64)
	if err != nil || versionErr != nil || version < 1 || r.ContentLength != 0 ||
		len(r.TransferEncoding) != 0 {
		writeError(w, errInvalid)
		return
	}
	result, err := s.runPrecheck(r.Context(), id, institution, version)
	respond(w, http.StatusOK, result, err)
}

// canPrecheck 判断监管或当前范围内的报备发起方能否对报备运行辅助预检。
// 参数 ctx、institution 携带经办范围，record 已经过现有实施报备详情归属检查。
// 返回值 该访问者可查看预检时为真。
func (s *Service) canPrecheck(ctx context.Context, institution string, record storedRecord) bool {
	if institution == "" && operatorOrganization(ctx) == "" {
		return true
	}
	return record.CreatedBy == s.reportOperator(ctx, institution)
}

// runPrecheck 在同一个读取快照核对报备版本、当前归属及关联证据。
// 参数 ctx 控制查询，id、institution 为报备和机构范围，version 为已查看的报备版本。
// 返回值 版本化的辅助结果或当前归属、版本及查询错误。
func (s *Service) runPrecheck(ctx context.Context, id, institution string, version int64) (materialPrecheck, error) {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	var result materialPrecheck
	err := pgx.BeginTxFunc(ctx, s.pool, options, func(tx pgx.Tx) error {
		record, err := getRecord(ctx, tx, id, institution, false)
		if err != nil {
			return err
		}
		if !s.canPrecheck(ctx, institution, record) {
			return errNotFound
		}
		if record.Version != version {
			return errStale
		}
		result, err = s.inspectPrecheck(ctx, tx, institution, record)
		return err
	})
	return result, err
}

// inspectPrecheck 汇集当前表单、名录、备案和可访问的材料证据。
// 参数 ctx、tx 为同一读取快照，institution 为已验证的机构范围，record 为当前报备。
// 返回值 具有可核对版本的辅助预检结果或查询错误。
func (s *Service) inspectPrecheck(ctx context.Context, tx pgx.Tx, institution string,
	record storedRecord,
) (materialPrecheck, error) {
	result := materialPrecheck{ReportVersion: record.Version, Findings: formPrecheck(record)}
	directoryFinding, err := checkImplementationDirectory(ctx, tx, record)
	if err != nil {
		return materialPrecheck{}, err
	}
	result.Findings = append(result.Findings, directoryFinding)
	filing, err := readPrecheckFiling(ctx, tx, record.FilingID, institution)
	if errors.Is(err, pgx.ErrNoRows) {
		result.Findings = append(result.Findings,
			precheckFinding{RuleID: "filing_registration", Status: "unavailable", Message: "当前范围无法核对关联备案。"},
			precheckFinding{RuleID: "filing_materials", Status: "unavailable", Message: "当前范围无法核对关联备案材料。"})
		return result, nil
	}
	if err != nil {
		return materialPrecheck{}, err
	}
	result.FilingVersion, result.MaterialRevision = &filing.Version, &filing.MaterialRevision
	findings, err := s.filingPrecheck(ctx, tx, record, filing)
	result.Findings = append(result.Findings, findings...)
	return result, err
}

// readPrecheckFiling 独立核对当前备案归属，避免仅凭报备关系越过材料下载范围。
// 参数 ctx、reader 控制查询，id 为关联备案，institution 为已验证的机构范围。
// 返回值 可访问备案的当前字段及版本，或未找到及数据库错误。
func readPrecheckFiling(ctx context.Context, reader rowReader, id, institution string) (precheckFiling, error) {
	var result precheckFiling
	err := reader.QueryRow(ctx, `SELECT id::text,number,system_name,organization_name,assessment_org,
	  version,material_revision,(status='approved' AND cancelled_at IS NULL)
	  FROM business_cases f WHERE id=$1 AND kind='filing' AND discarded_at IS NULL
	  AND ($2::text='' OR assessment_org COLLATE "C"=$2)
	  AND ($3::text='' OR organization_name COLLATE "C"=$3)
	  AND `+access.FilingDistrictScope("f.id", "$4", false), id, institution, operatorOrganization(ctx), access.Districts(ctx)).
		Scan(&result.ID, &result.Number, &result.SystemName, &result.OrganizationName,
			&result.AssessmentOrg, &result.Version, &result.MaterialRevision, &result.Active)
	return result, err
}
