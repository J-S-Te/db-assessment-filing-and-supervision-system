package cases

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/access"
	"grade-business/internal/filingpolicy"
)

const recordColumns = `
	c.id::text, c.number, COALESCE(f.system_name,c.system_name),
	CASE WHEN c.kind='filing' THEN c.organization_name
	     ELSE COALESCE(c.directory_snapshot#>>'{unit,scopeName}',f.organization_name,c.organization_name)
	END, c.assessment_org,
	c.level, COALESCE(f.number,''), c.risk, c.status,
	CASE WHEN c.kind = 'report' THEN c.submitted_at END, c.cancelled_at,
	CASE WHEN c.operator_origin THEN 'institution' ELSE '' END
`
const recordJoin = " FROM business_cases c LEFT JOIN business_cases f ON f.id=c.filing_id"
const recordFilters = `
	 WHERE c.kind=$1 AND c.discarded_at IS NULL
	   AND ($2='' OR c.status=$2)
	   AND ($3='' OR strpos(lower(concat_ws(' ',c.number,f.number,
	     COALESCE(f.system_name,c.system_name),COALESCE(f.organization_name,c.organization_name),
	     c.assessment_org)),lower($3))>0)
	   AND ($4='' OR ($4='active' AND c.cancelled_at IS NULL AND c.status<>'withdrawn')
	      OR ($4='cancelled' AND c.cancelled_at IS NOT NULL))
	   AND ($5='' OR c.risk=$5)
`

// caseDistrictScope 为备案与测评查询接入名录中的现行辖区。
// 参数 parameter 为查询中绑定标准区县列表的 SQL 参数位置。
// 返回值 匹配可信单位标识及当前区县的 SQL 条件。
func caseDistrictScope(parameter string) string {
	return ` AND (` + parameter + `::text[] IS NULL OR EXISTS (
	    SELECT 1 FROM directory_parties u
	    WHERE u.id=COALESCE(f.directory_unit_id,c.directory_unit_id)
	      AND u.kind='unit' AND u.district=ANY(` + parameter + `::text[])))`
}

// parseListQuery 解析受限分页参数并验证允许的筛选条件。
// 参数 query 为请求 URL 查询参数，validFilterStatus 校验当前集合的状态。
// 返回值 解码后的列表查询或参数错误。
func parseListQuery(query url.Values, validFilterStatus func(string) bool) (listQuery, error) {
	input := listQuery{keyword: strings.TrimSpace(query.Get("q")), status: query.Get("status"),
		lifecycle: query.Get("lifecycle"), risk: query.Get("risk"), limit: 20}
	for key, values := range query {
		switch key {
		case "q", "status", "cursor", "limit", "lifecycle", "risk":
		default:
			return listQuery{}, ErrInvalid
		}
		if len(values) != 1 {
			return listQuery{}, ErrInvalid
		}
	}
	validLifecycle := input.lifecycle == "" || input.lifecycle == "active" || input.lifecycle == "cancelled"
	if !validLifecycle || (input.risk != "" && !validRisk(input.risk)) ||
		!validFilterStatus(input.status) || utf8.RuneCountInString(input.keyword) > 120 ||
		strings.ContainsRune(input.keyword, 0) {
		return listQuery{}, ErrInvalid
	}
	if raw, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			return listQuery{}, ErrInvalid
		}
		input.limit = limit
	}
	cursor, err := parseCursor(query.Get("cursor"))
	input.cursor = cursor
	return input, err
}

// parseCursor 解码按创建时间和 UUID 排序的分页位置。
// 参数 raw 为上页返回的编码位置，空值表示第一页。
// 返回值 分页位置或无效游标错误。
func parseCursor(raw string) (listCursor, error) {
	if raw == "" {
		return listCursor{}, nil
	}
	if len(raw) > 110 {
		return listCursor{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return listCursor{}, ErrInvalid
	}
	stamp, id, ok := strings.Cut(string(payload), ",")
	if !ok || !validID(id) {
		return listCursor{}, ErrInvalid
	}
	created, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || created.IsZero() {
		return listCursor{}, ErrInvalid
	}
	return listCursor{CreatedAt: created, ID: id}, nil
}

// encodeCursor 生成与当前排序对应的下一页位置。
// 参数 item 为上一页最后一个已返回条目的时间和值。
// 返回值 适合放入 URL 的分页游标。
func encodeCursor(item listCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(item.CreatedAt.UTC().Format(time.RFC3339Nano) + "," + item.ID))
}

// scanFields 提供记录公共列的扫描目标。
// 参数 row 为待填充的列表或详情公共部分。
// 返回值 与 recordColumns 顺序对应的扫描指针。
func scanFields(row *recordRow) []any {
	return []any{
		&row.ID, &row.Number, &row.SystemName, &row.OrganizationName, &row.AssessmentOrg,
		&row.Level, &row.FilingNumber, &row.Risk, &row.Status, &row.SubmittedAt, &row.CancelledAt, &row.InitialReviewAudience,
	}
}

// list 查询指定业务记录，使用稳定游标和最小列表投影。
// 参数 ctx 携带可选运营单位，kind 为业务类型，input 为筛选与分页条件。
// 返回值 一页最小业务字段和可选下一页游标。
func (s *Service) list(ctx context.Context, kind string, input listQuery) (listResponse, error) {
	var created *time.Time
	var id *string
	if input.cursor.ID != "" {
		created, id = &input.cursor.CreatedAt, &input.cursor.ID
	}
	query := "SELECT " + recordColumns + `,c.created_at,
	    CASE WHEN $9<>'' AND c.kind='filing' AND NOT c.operator_origin
	         AND c.cancelled_at IS NULL AND c.status<>'withdrawn' THEN c.version END` + recordJoin + recordFilters + `
	   AND ($6::timestamptz IS NULL OR (c.created_at,c.id)<($6,$7::uuid))
	   AND ($9='' OR c.assessment_org=$9)
	   AND ($10='' OR (c.kind='filing' AND c.organization_name=$10))
	   AND (NOT $11::bool OR (c.operator_origin AND c.status='pending_initial' AND NOT EXISTS(
	       SELECT 1 FROM filing_withdrawal_requests w
	       WHERE w.resource='filings' AND w.resource_id=c.id AND w.status='pending')))` +
		caseDistrictScope("$12") + `
	 ORDER BY c.created_at DESC,c.id DESC
	 LIMIT $8`
	rows, err := s.db.Query(ctx, query, kind, input.status, input.keyword, input.lifecycle, input.risk,
		created, id, input.limit+1, input.institution, operatorFilingScope(ctx), input.assignedReview, access.Districts(ctx))
	if err != nil {
		return listResponse{}, err
	}
	defer rows.Close()
	return collectList(rows, input.limit)
}

// collectList 将查询记录转换为列表响应并摘取下一页位置。
// 参数 rows 为已排序查询游标，limit 为最多返回的条数。
// 返回值 列表响应或数据库迭代错误。
func collectList(rows pgx.Rows, limit int) (listResponse, error) {
	result := listResponse{Items: make([]recordRow, 0, limit)}
	var last listCursor
	for rows.Next() {
		if len(result.Items) == limit {
			result.NextCursor = encodeCursor(last)
			break
		}
		var item recordRow
		if err := rows.Scan(append(scanFields(&item), &last.CreatedAt, &item.BatchVersion)...); err != nil {
			return listResponse{}, err
		}
		last.ID = item.ID
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

// detail 返回指定记录的完整业务详情和可操作动作。
// 参数 ctx 为请求上下文，kind 为记录类型，id 为记录标识。
// 返回值 类型匹配的详情，或未找到及数据库错误。
func (s *Service) detail(ctx context.Context, kind, id string) (recordDetail, error) {
	return s.detailInScope(ctx, kind, id, "")
}

// detailInScope 按本地办理范围查询记录、材料要求和回执状态。
// 参数 ctx 携带可选运营单位，kind、id 指定记录，institution 为可选机构名称。
// 返回值 匹配的详情或业务归属及数据库错误。
func (s *Service) detailInScope(ctx context.Context, kind, id, institution string) (recordDetail, error) {
	if !validID(id) {
		return recordDetail{}, ErrNotFound
	}
	var item recordDetail
	var profile string
	var filingSubmittedAt *time.Time
	row := s.db.QueryRow(ctx, "SELECT "+recordColumns+
		`, COALESCE(c.filing_id::text,''),c.version,c.submission_profile,c.operator_origin,
		   CASE WHEN $4<>'' THEN c.submitted_at END,
		   CASE WHEN $4<>'' THEN EXISTS(
		       SELECT 1 FROM case_review_events e WHERE e.case_id=c.id
		         AND COALESCE(e.merge_history_scope,c.organization_name)=$4 AND `+filingReceiptEvent+`
		   ) END,c.directory_person_id::text,c.directory_snapshot,
		   c.classification_assessment,(c.operator_origin AND EXISTS(SELECT 1 FROM filing_withdrawal_requests w
		      WHERE w.resource='filings' AND w.resource_id=c.id AND w.status='pending'))`+recordJoin+`
		 WHERE c.kind=$1 AND c.id=$2 AND c.discarded_at IS NULL
		   AND ($3='' OR c.assessment_org=$3)
		   AND ($4='' OR (c.kind='filing' AND c.organization_name=$4))`+caseDistrictScope("$5"),
		kind, id, institution, operatorFilingScope(ctx), access.Districts(ctx))
	err := row.Scan(append(scanFields(&item.recordRow),
		&item.FilingID, &item.Version, &profile, &item.operatorOrigin, &filingSubmittedAt, &item.ReceiptAvailable,
		&item.AssessmentPersonID, &item.DirectorySnapshot, &item.Classification, &item.withdrawalPending)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return recordDetail{}, ErrNotFound
	}
	if err != nil {
		return recordDetail{}, err
	}
	item.AllowedActions = allowedActions(item.Status)
	actor, authenticated := access.Current(ctx)
	if item.withdrawalPending ||
		(item.operatorOrigin && item.Status == pendingInitial && (!authenticated || (actor.Role != access.SystemAdmin && actor.Role != access.Regulator))) {
		item.AllowedActions = []string{}
	}
	if profile == institutionSubmissionProfile {
		item.RequiredMaterials = institutionReportMaterials
	}
	if filingSubmittedAt != nil {
		item.SubmittedAt = filingSubmittedAt
	}
	if kind == filing {
		policy, policyErr := filingpolicy.Load(ctx, s.db, false)
		if policyErr != nil {
			return recordDetail{}, policyErr
		}
		item.PolicyMaterials = policy.For(filingpolicy.Initial)
	}
	return item, nil
}

// ensureRecord 确认 URL 上的业务类型和标识共同对应一条真实记录。
// 参数 ctx 携带可选运营单位，kind 为类型，id 为记录标识。
// 返回值 未找到或数据库错误，匹配时返回 nil。
func (s *Service) ensureRecord(ctx context.Context, kind, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM business_cases WHERE kind=$1 AND id=$2
		  AND ($3='' OR (kind='filing' AND organization_name=$3))
	)`, kind, id, operatorFilingScope(ctx)).Scan(&exists)
	if err == nil && !exists {
		return ErrNotFound
	}
	return err
}

// history 返回指定备案或测评的有序审核审计。
// 参数 ctx 携带可选运营单位，kind、id 为业务类型和标识，institution 为可选机构范围。
// 返回值 实际审核留痕或归属、读取错误。
func (s *Service) history(ctx context.Context, kind, id, institution string) ([]reviewEvent, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(e.action,''),COALESCE(e.from_status,''),COALESCE(e.to_status,''),
		       COALESCE(e.operator,''),COALESCE(e.comment,''),COALESCE(e.occurred_at,c.created_at),
		       e.directory_snapshot
		FROM business_cases c LEFT JOIN business_cases f ON f.id=c.filing_id
		LEFT JOIN case_review_events e ON c.id=e.case_id
		  AND ($3='' OR COALESCE(e.merge_history_scope,c.organization_name)=$3)
		WHERE c.id=$1 AND c.kind=$2 AND c.discarded_at IS NULL
		  AND ($3='' OR (c.kind='filing' AND (c.organization_name=$3 OR e.id IS NOT NULL)))
		  AND ($4='' OR c.assessment_org=$4)`+caseDistrictScope("$5")+`
		ORDER BY e.id DESC`, id, kind, operatorFilingScope(ctx), institution, access.Districts(ctx))
	return collectReviewHistory(rows, err)
}

// collectReviewHistory 区分归属不匹配、无审核事件以及实际审核历史。
// 参数 rows 与 err 为同一快照联接业务记录和审核事件的数据库游标及错误。
// 返回值 审核事件、空历史或查询及业务归属错误。
func collectReviewHistory(rows pgx.Rows, err error) ([]reviewEvent, error) {
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reviewEvent])
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	if items[0].Action == "" {
		return items[:0], nil
	}
	return items, nil
}
