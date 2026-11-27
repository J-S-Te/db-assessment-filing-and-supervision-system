package supervision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxBatchSize = 100

var errBatchTooLarge = errors.New("supervision batch too large")

type batchCriteria struct {
	District        string   `json:"district,omitempty"`
	Levels          []string `json:"levels,omitempty"`
	Title           string   `json:"title"`
	DueOn           string   `json:"dueOn"`
	TemplateTaskID  string   `json:"templateTaskId"`
	TemplateVersion int64    `json:"templateVersion"`
}

type batchConfirm struct {
	batchCriteria
	Token string `json:"token"`
}

type batchRecipient struct {
	FilingID         string `json:"filingId"`
	Number           string `json:"number"`
	SystemName       string `json:"systemName"`
	OrganizationName string `json:"organizationName"`
	Status           string `json:"status"`
	ExistingTaskID   string `json:"existingTaskId,omitempty"`
}

type batchCandidate struct {
	batchRecipient
	DirectoryID      string
	DirectoryVersion int64
	FilingVersion    int64
}

type batchPreview struct {
	Items         []batchRecipient `json:"items"`
	TotalEligible int              `json:"totalEligible"`
	Token         string           `json:"token,omitempty"`
}

type batchPlan struct {
	input   batchCriteria
	items   []batchCandidate
	sources [2]string
}

type batchDelivery struct {
	FilingID string `json:"filingId"`
	TaskID   string `json:"taskId"`
	Status   string `json:"status"`
}

type batchResult struct {
	ID             string          `json:"id"`
	Items          []batchDelivery `json:"items"`
	PublishedCount int             `json:"publishedCount"`
}

type batchTemplateOption struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Version       int64    `json:"version"`
	TemplateNames []string `json:"templateNames"`
}

// validateBatchCriteria 规范区县及等级，要求有效模板版本及明确收件条件。
// 参数 input 为请求筛选和任务公用信息。
// 返回值 格式和范围条件均有效时为真。
func validateBatchCriteria(input *batchCriteria) bool {
	input.District = strings.TrimSpace(input.District)
	input.Title = strings.TrimSpace(input.Title)
	input.TemplateTaskID = strings.ToLower(input.TemplateTaskID)
	validDistrict := input.District == "" || validText(input.District, 120)
	date, err := time.Parse(time.DateOnly, input.DueOn)
	if !validDistrict || (input.District == "" && len(input.Levels) == 0) || len(input.Levels) > 5 ||
		!validText(input.Title, 200) || !validID(input.TemplateTaskID) || input.TemplateVersion < 1 ||
		err != nil || date.Year() < 1 {
		return false
	}
	slices.Sort(input.Levels)
	for i, level := range input.Levels {
		if !slices.Contains([]string{"1", "2", "3", "4", "5"}, level) || (i > 0 && level == input.Levels[i-1]) {
			return false
		}
	}
	if len(input.Levels) == 0 {
		input.Levels = []string{}
	}
	return true
}

// supervisionHash 计算可重放的输入摘要，输入为模块内固定结构。
// 参数 value 是可编码、没有动态资源的业务结构。
// 返回值 十六进制摘要或序列化错误。
func supervisionHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// validBatchToken 限制确认时接受的预览摘要格式。
// 参数 token 是客户端返回的预览摘要。
// 返回值 摘要为有效小写十六进制时为真。
func validBatchToken(token string) bool {
	if len(token) != 64 || token != strings.ToLower(token) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

// buildBatchPlan 在当前快照核对模板、截止日期和名录内收件范围。
// 参数 ctx、tx 控制事务，input 为筛选条件，lock 在最终下发时锁定相关记录。
// 返回值 不会扩大接收范围的计划或业务及查询错误。
func buildBatchPlan(ctx context.Context, tx pgx.Tx, input batchCriteria, lock bool) (batchPlan, error) {
	plan := batchPlan{input: input}
	var future bool
	err := tx.QueryRow(ctx, `SELECT $1::date>(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`,
		input.DueOn).Scan(&future)
	if err != nil {
		return plan, err
	}
	if !future {
		return plan, errInput
	}
	plan.sources, err = batchMaterialSources(ctx, tx, input, lock)
	if err != nil {
		return plan, err
	}
	plan.items, err = selectBatchCandidates(ctx, tx, input, lock)
	return plan, err
}

// batchMaterialSources 绑定同一任务的已核验模板及确切版本。
// 参数 ctx、tx 为当前事务，input 指模板任务和版本，lock 为提交时的锁定标记。
// 返回值 自查及整改汇总模板标识，或缺失或被更新的提示。
func batchMaterialSources(ctx context.Context, tx pgx.Tx, input batchCriteria, lock bool) ([2]string, error) {
	var result [2]string
	record, err := loadTask(ctx, tx, input.TemplateTaskID, "", lock)
	if err != nil {
		return result, err
	}
	if record.Status == "cancelled" || record.Version != input.TemplateVersion {
		return result, errState
	}
	first, second, err := requiredMaterials(ctx, tx, record, inspectionTemplate, summaryTemplate)
	return [2]string{first.ID, second.ID}, err
}

const batchCandidatesSQL = `
	SELECT f.id::text,f.number,f.system_name,f.organization_name,
	       CASE WHEN old.id IS NULL THEN 'eligible' ELSE 'existing' END,COALESCE(old.id::text,''),
	       d.id::text,d.version,f.version
	FROM business_cases f
	JOIN directory_parties d ON d.kind='unit' AND d.status='active'
	  AND d.merged_into_id IS NULL AND d.scope_name COLLATE "C"=f.organization_name
	  AND (f.directory_unit_id IS NULL OR f.directory_unit_id=d.id)
	LEFT JOIN supervision_tasks old ON old.filing_id=f.id AND old.lead_alert_id IS NULL
	  AND old.status<>'cancelled' AND old.cycle_year=EXTRACT(YEAR FROM $3::date)::int
	WHERE ` + eligibleFiling + `
	  AND ($1='' OR d.district COLLATE "C"=$1)
	  AND (cardinality($2::text[])=0 OR f.level=ANY($2::text[]))
	ORDER BY f.id LIMIT $4`

// selectBatchCandidates 查询活跃名录、保护等级及本年度已有任务。
// 参数 ctx、tx 为查询事务，input 是收件条件，lock 在确认时锁定备案及其名录。
// 返回值 上限内按标识稳定排序的收件人，或要求缩小范围的提示。
func selectBatchCandidates(ctx context.Context, tx pgx.Tx, input batchCriteria, lock bool) ([]batchCandidate, error) {
	query := batchCandidatesSQL
	if lock {
		query += " FOR SHARE OF f,d"
	}
	rows, err := tx.Query(ctx, query, input.District, input.Levels, input.DueOn, maxBatchSize+1)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[batchCandidate])
	if err == nil && len(items) > maxBatchSize {
		return nil, errBatchTooLarge
	}
	return items, err
}

// digest 同时绑定预览的业务条件、文件和收件快照以防止确认时范围漂移。
// 参数 无，当前计划已经完成目录和模板核验。
// 返回值 可供客户端确认的摘要或编码错误。
func (plan batchPlan) digest() (string, error) {
	return supervisionHash(struct {
		Criteria batchCriteria
		Items    []batchCandidate
		Sources  [2]string
	}{plan.input, plan.items, plan.sources})
}

// previewBatch 返回不产生通知的只读预览，范围中有可发记录时给出确认摘要。
// 参数 ctx 控制查询，input 为已校验筛选。
// 返回值 预览或业务条件及数据库错误。
func (s *Service) previewBatch(ctx context.Context, input batchCriteria) (batchPreview, error) {
	result := batchPreview{Items: []batchRecipient{}}
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
		func(tx pgx.Tx) error {
			plan, err := buildBatchPlan(ctx, tx, input, false)
			if err != nil {
				return err
			}
			for _, recipient := range plan.items {
				result.Items = append(result.Items, recipient.batchRecipient)
				if recipient.Status == "eligible" {
					result.TotalEligible++
				}
			}
			if result.TotalEligible > 0 {
				result.Token, err = plan.digest()
			}
			return err
		})
	return result, err
}

// batchTemplateOptions 提供存在完整模板的任务和实际可复用文件名。
// 参数 ctx 控制查询。
// 返回值 最近二十份有效模板来源和校验错误。
func (s *Service) batchTemplateOptions(ctx context.Context) (page[batchTemplateOption], error) {
	rows, err := s.db.Query(ctx, `SELECT t.id::text,t.title,t.version,ARRAY[i.file_name,r.file_name]
		FROM supervision_tasks t
		JOIN LATERAL (SELECT file_name FROM supervision_attachments
		  WHERE task_id=t.id AND kind='inspection_template' ORDER BY task_version DESC LIMIT 1) i ON true
		JOIN LATERAL (SELECT file_name FROM supervision_attachments
		  WHERE task_id=t.id AND kind='summary_template' ORDER BY task_version DESC LIMIT 1) r ON true
		WHERE t.status<>'cancelled' ORDER BY t.sequence DESC LIMIT 20`)
	if err != nil {
		return page[batchTemplateOption]{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[batchTemplateOption])
	return page[batchTemplateOption]{Items: items}, err
}
