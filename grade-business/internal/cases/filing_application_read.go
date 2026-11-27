package cases

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/filingpolicy"
)

const applicationColumns = `id::text,filing_id::text,filing_number,
	before_values->>'systemName',before_values->>'organizationName',application_type,status,created_at,
	CASE WHEN operator_origin THEN 'institution' ELSE '' END`

type applicationQuery struct {
	listQuery
	kind, filingID string
	organization   string
}

type applicationPage struct {
	Items      []applicationRow `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// parseApplicationQuery 校验申请列表独有条件并复用通用有界分页参数。
// 参数 query 为申请列表的 URL 查询对象。
// 返回值 申请筛选和分页配置，或参数错误。
func parseApplicationQuery(query url.Values) (applicationQuery, error) {
	if len(query["filingId"]) > 1 || len(query["type"]) > 1 || query.Has("lifecycle") {
		return applicationQuery{}, ErrInvalid
	}
	input := applicationQuery{kind: query.Get("type"), filingID: query.Get("filingId")}
	if (input.kind != "" && input.kind != applicationChange && input.kind != applicationCancel) ||
		(input.filingID != "" && !validID(input.filingID)) {
		return applicationQuery{}, ErrInvalid
	}
	query.Del("type")
	query.Del("filingId")
	common, err := parseListQuery(query, validApplicationStatus)
	input.listQuery = common
	return input, err
}

// validApplicationStatus 校验备案申请适用的列表状态筛选条件。
// 参数 status 为待验证的状态，空字符串代表全部状态。
// 返回值 申请列表可使用该条件时为真。
func validApplicationStatus(status string) bool {
	return status == applicationWithdrawn || validStatus(status)
}

// scanApplicationFields 指定申请列表公共列的接收位置。
// 参数 row 为待填充的公开列表条目。
// 返回值 与申请公共列顺序一致的扫描指针。
func scanApplicationFields(row *applicationRow) []any {
	return []any{&row.ID, &row.FilingID, &row.FilingNumber, &row.SystemName,
		&row.OrganizationName, &row.Type, &row.Status, &row.CreatedAt, &row.InitialReviewAudience}
}

// listApplications 返回按筛选条件查询的申请列表及稳定位点。
// 参数 ctx 为请求上下文，input 为类型、备案、状态、关键词和分页条件。
// 返回值 本页记录和下一页游标，或读取错误。
func (s *Service) listApplications(ctx context.Context, input applicationQuery) (applicationPage, error) {
	var created *time.Time
	var id *string
	if input.cursor.ID != "" {
		created, id = &input.cursor.CreatedAt, &input.cursor.ID
	}
	var filingID *string
	if input.filingID != "" {
		filingID = &input.filingID
	}
	rows, err := s.db.Query(ctx, "SELECT "+applicationColumns+` FROM filing_applications
		WHERE ($1='' OR application_type=$1) AND ($2='' OR status=$2)
		  AND ($3::uuid IS NULL OR filing_id=$3)
		  AND ($4='' OR strpos(lower(concat_ws(' ',filing_number,before_values->>'systemName',
		      before_values->>'organizationName')),lower($4))>0)
		  AND ($5::timestamptz IS NULL OR (created_at,id)<($5,$6::uuid))
		  AND ($8='' OR before_values->>'assessmentOrg'=$8)
		  AND ($9='' OR before_values->>'organizationName'=$9)
		  AND (NOT $10::bool OR (operator_origin AND status='pending_initial' AND NOT EXISTS(
		      SELECT 1 FROM filing_withdrawal_requests w
		      WHERE w.resource='filing-applications' AND w.resource_id=filing_applications.id AND w.status='pending')))
		ORDER BY created_at DESC,id DESC LIMIT $7`,
		input.kind, input.status, filingID, input.keyword, created, id, input.limit+1,
		input.institution, input.organization, input.assignedReview)
	if err != nil {
		return applicationPage{}, err
	}
	defer rows.Close()
	return collectApplications(rows, input.limit)
}

// collectApplications 读取当前页并保存下一页的上一条记录位置。
// 参数 rows 为已排序的数据库查询结果，limit 为最大公开条数。
// 返回值 列表输出，或扫描与迭代错误。
func collectApplications(rows pgx.Rows, limit int) (applicationPage, error) {
	result := applicationPage{Items: make([]applicationRow, 0, limit)}
	for rows.Next() {
		if len(result.Items) == limit {
			last := result.Items[limit-1]
			result.NextCursor = encodeCursor(listCursor{CreatedAt: last.CreatedAt, ID: last.ID})
			break
		}
		var item applicationRow
		if err := rows.Scan(scanApplicationFields(&item)...); err != nil {
			return applicationPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// application 按标识查询申请详情及其审核时使用的前后值。
// 参数 ctx 为请求上下文，id 为备案变更或注销申请标识。
// 返回值 该申请详细信息或未找到、读取错误。
func (s *Service) application(ctx context.Context, id string) (applicationDetail, error) {
	return s.applicationInScope(ctx, id, "")
}

// applicationInScope 按不可变的原备案快照核对已选择端的归属并返回申请。
// 参数 ctx 含可选运营单位，id 为申请标识，institution 为可选原发起机构。
// 返回值 申请及前后差异，或查询和归属错误。
func (s *Service) applicationInScope(ctx context.Context, id, institution string) (applicationDetail, error) {
	if !validID(id) {
		return applicationDetail{}, ErrNotFound
	}
	var detail applicationDetail
	var before, after filingValues
	var operatorOrigin, hasReceipt, currentOwner, withdrawalPending bool
	organization := applicationOrganization(ctx)
	row := s.db.QueryRow(ctx, "SELECT "+applicationColumns+`,reason,version,applied_at,before_values,after_values,
		operator_origin,implementation_id::text,
		EXISTS(SELECT 1 FROM filing_application_review_events e WHERE e.application_id=filing_applications.id
		AND `+applicationReceiptEvent+`),
		EXISTS(SELECT 1 FROM business_cases f WHERE f.id=filing_applications.filing_id AND f.kind='filing'
		AND f.organization_name=$3 AND f.status='approved' AND f.cancelled_at IS NULL
		AND f.version=filing_applications.filing_version),
		(operator_origin AND EXISTS(SELECT 1 FROM filing_withdrawal_requests w
		  WHERE w.resource='filing-applications' AND w.resource_id=filing_applications.id
		    AND w.status='pending')) FROM filing_applications
		WHERE id=$1 AND ($2='' OR before_values->>'assessmentOrg'=$2)
		AND ($3='' OR before_values->>'organizationName'=$3)`, id, institution, organization)
	err := row.Scan(append(scanApplicationFields(&detail.applicationRow), &detail.Reason,
		&detail.Version, &detail.AppliedAt, &before, &after,
		&operatorOrigin, &detail.ImplementationID, &hasReceipt, &currentOwner, &withdrawalPending)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return applicationDetail{}, ErrNotFound
	}
	if err != nil {
		return applicationDetail{}, err
	}
	detail.AllowedActions = allowedActions(detail.Status)
	actor, authenticated := access.Current(ctx)
	if operatorOrigin && (institution != "" || (detail.Status == pendingInitial &&
		(!authenticated || actor.Role != access.SystemAdmin))) {
		detail.AllowedActions = initialReviewActions(detail.Status, institution != "" && !authenticated)
	} else if institution != "" {
		detail.AllowedActions = institutionEditActions(detail.Status)
	}
	if editable(detail.Status) && (institution == "" || !operatorOrigin) {
		detail.AllowedActions = append(detail.AllowedActions, withdrawApplication)
	}
	if withdrawalPending {
		detail.AllowedActions = []string{}
	}
	detail.Changes = applicationDiffs(before, after)
	if operatorOrigin || (organization != "" && editable(detail.Status)) {
		detail.RequiredMaterials = []string{requiredApplicationMaterial(detail.Type)}
		policy, policyErr := filingpolicy.Load(ctx, s.db, false)
		if policyErr != nil {
			return applicationDetail{}, policyErr
		}
		detail.PolicyMaterials = policy.For(detail.Type)
	}
	if organization != "" {
		detail.ReceiptAvailable = &hasReceipt
		detail.AllowedActions = []string{}
		if currentOwner && editable(detail.Status) {
			detail.AllowedActions = append(institutionEditActions(detail.Status), withdrawApplication)
		}
		if currentOwner && detail.Type == applicationChange && detail.ImplementationID == nil &&
			before.AssessmentOrg != after.AssessmentOrg {
			detail.RetestImplementation, err = s.availableApplicationRetest(ctx, detail.FilingID, after.AssessmentOrg)
			if err != nil {
				return applicationDetail{}, err
			}
		}
	}
	return detail, nil
}

// applicationHistory 查询指定申请的审核留痕，草稿返回空列表。
// 参数 ctx 含可选运营单位，id 为备案申请标识，institution 为可选原备案机构。
// 返回值 最新记录在前的审核历史，或不存在及读取错误。
func (s *Service) applicationHistory(ctx context.Context, id, institution string) ([]reviewEvent, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(e.action,''),COALESCE(e.from_status,''),COALESCE(e.to_status,''),
		       COALESCE(e.operator,''),COALESCE(e.comment,''),COALESCE(e.occurred_at,a.created_at),NULL::jsonb
		FROM filing_applications a LEFT JOIN filing_application_review_events e ON a.id=e.application_id
		WHERE a.id=$1 AND ($2='' OR a.before_values->>'organizationName'=$2)
		  AND ($3='' OR a.before_values->>'assessmentOrg'=$3)
		ORDER BY e.id DESC`, id, applicationOrganization(ctx), institution)
	return collectReviewHistory(rows, err)
}
