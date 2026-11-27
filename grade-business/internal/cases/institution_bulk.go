package cases

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/directoryref"
)

const institutionBatchLimit = 20

type filingCopyInput struct {
	versionInput
	RequestID string `json:"requestId"`
	Number    string `json:"number"`
}

type filingBatchInput struct {
	RequestID string            `json:"requestId"`
	Action    string            `json:"action"`
	Items     []filingBatchItem `json:"items"`
}

type filingBatchItem struct {
	ID string `json:"id"`
	versionInput
}

type filingBatchOutcome struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Version int64  `json:"version,omitempty"`
	Code    string `json:"code,omitempty"`
}

// valid 校验并规范化一页内的批量命令，拒绝重复单据及无效版本。
// 参数 无，使用从机构请求读取的命令。
// 返回值 整批输入可以办理为真。
func (input *filingBatchInput) valid() bool {
	if !validID(input.RequestID) || (input.Action != submit && input.Action != "discard") ||
		len(input.Items) == 0 || len(input.Items) > institutionBatchLimit {
		return false
	}
	input.RequestID = strings.ToLower(input.RequestID)
	seen := make(map[string]bool, len(input.Items))
	for i := range input.Items {
		item := &input.Items[i]
		item.ID = strings.ToLower(item.ID)
		if !validID(item.ID) || item.ExpectedVersion < 1 || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
	}
	return true
}

// copyInstitutionFiling 用途为从本机构有效记录复制表单，创建带可选预填编号的独立草稿。
// 参数 ctx、tx 为受控事务，scope、id 为机构及源记录，input 为预填编号及源版本。
// 返回值 草稿标识与版本，或归属、版本及输入错误。
func copyInstitutionFiling(ctx context.Context, tx pgx.Tx, scope, id string, input filingCopyInput) (mutation, error) {
	form := filingInput{Number: input.Number, AssessmentOrg: scope}
	var version int64
	err := tx.QueryRow(ctx, `SELECT system_name,organization_name,level,version
		FROM business_cases WHERE id=$1 AND kind='filing' AND assessment_org=$2
		  AND discarded_at IS NULL AND NOT operator_origin AND cancelled_at IS NULL AND status<>'withdrawn'
		FOR SHARE`, id, scope).Scan(&form.SystemName, &form.OrganizationName, &form.Level, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return mutation{}, ErrNotFound
	}
	if err != nil {
		return mutation{}, err
	}
	if version != input.ExpectedVersion {
		return mutation{}, ErrConflict
	}
	fields, err := parseFilingFields(form, false)
	if err != nil {
		return mutation{}, err
	}
	if _, err := directoryref.Resolve(ctx, tx, fields.organizationName, fields.assessmentOrg, "", false); err != nil {
		return mutation{}, err
	}
	return createCaseInTx(ctx, tx, filing, fields, scope)
}

// runInstitutionBatch 按固定记录顺序加锁，返回与客户端录入顺序一致的逐条结果。
// 参数 ctx、tx 为受控事务，scope 为当前机构，input 为已经校验的批量命令。
// 返回值 已办理的最小结果，或应回滚整个批次的基础设施错误。
func (s *Service) runInstitutionBatch(ctx context.Context, tx pgx.Tx, scope string,
	input filingBatchInput,
) (itemResponse[filingBatchOutcome], error) {
	result := itemResponse[filingBatchOutcome]{Items: make([]filingBatchOutcome, len(input.Items))}
	order := make([]int, len(input.Items))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return strings.Compare(input.Items[a].ID, input.Items[b].ID) })
	for _, i := range order {
		item, err := s.runInstitutionItem(ctx, tx, scope, input.Action, input.Items[i])
		if err != nil {
			return itemResponse[filingBatchOutcome]{}, err
		}
		result.Items[i] = item
	}
	return result, nil
}

// runInstitutionItem 在保存点内复用既有送审状态机，单条业务冲突不影响其他记录。
// 参数 ctx、parent、scope 为请求、批次事务与机构，action、item 为动作及对应单据。
// 返回值 对应条目的办理结果，或需让整个批次回滚的基础设施错误。
func (s *Service) runInstitutionItem(ctx context.Context, parent pgx.Tx, scope, action string,
	item filingBatchItem,
) (filingBatchOutcome, error) {
	out := filingBatchOutcome{ID: item.ID}
	err := pgx.BeginFunc(ctx, parent, func(tx pgx.Tx) error {
		if action == "discard" {
			out.Outcome = "discarded"
			version, err := discardInstitutionFiling(ctx, tx, scope, item)
			out.Version = version
			return err
		}
		out.Outcome = "submitted"
		updated, err := s.changeStatusInTx(ctx, tx, filing, item.ID,
			reviewInput{versionInput: item.versionInput, Decision: submit, institution: scope})
		out.Version = updated.Version
		return err
	})
	if err == nil {
		return out, nil
	}
	code := filingBatchRejection(err)
	if code == "" {
		return filingBatchOutcome{}, err
	}
	return filingBatchOutcome{ID: item.ID, Outcome: "rejected", Code: code}, nil
}

// filingBatchRejection 将可继续处理的业务错误转成安全的逐条结果码。
// 参数 err 为已经退出保存点的业务或数据库错误。
// 返回值 已知业务原因；空值要求上层回滚整个批次。
func filingBatchRejection(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrMaterials):
		return "materials_required"
	case errors.Is(err, ErrOpenMaterialIssues):
		return "material_issues_open"
	case errors.Is(err, errWithdrawalPending):
		return "withdrawal_pending"
	default:
		return ""
	}
}

// discardInstitutionFiling 从当前列表隐藏从未送审的机构草稿，并保留历史文件。
// 参数 ctx、tx 为保存点事务，scope、item 指定机构、单据和预期版本。
// 返回值 删除后的版本，或权限、状态及异常关闭错误。
func discardInstitutionFiling(ctx context.Context, tx pgx.Tx, scope string, item filingBatchItem) (int64, error) {
	current, err := lockInstitutionRecord(ctx, tx, filing, item.ID, scope)
	if err != nil {
		return 0, err
	}
	if current.version != item.ExpectedVersion || current.status != draft || current.operatorOrigin {
		return 0, ErrConflict
	}
	var version int64
	err = tx.QueryRow(ctx, `UPDATE business_cases c
		SET discarded_at=clock_timestamp(),version=version+1
		WHERE id=$1 AND submitted_at IS NULL AND cancelled_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM business_cases r WHERE r.filing_id=c.id AND r.discarded_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM case_review_events e WHERE e.case_id=c.id)
		RETURNING version`, item.ID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	return version, closeMaterialIssues(ctx, tx, item.ID, nil, nil)
}
