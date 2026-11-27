package remediations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const joins = `FROM remediation_plans p JOIN business_cases r ON r.id=p.report_id AND r.kind='report'
	JOIN business_cases f ON f.id=r.filing_id AND f.kind='filing'`

const planFilingNumber = `COALESCE(p.frozen_filing_number,f.number)`
const planSystemName = `COALESCE(p.frozen_system_name,f.system_name)`

const projection = `p.id::text,r.id::text,r.number,` + planFilingNumber + `,` + planSystemName + `,
	COALESCE(p.frozen_organization_name,f.organization_name),
	to_char(p.deadline,'YYYY-MM-DD'),p.status,p.submission,p.submitted_at,
	jsonb_array_length(jsonb_path_query_array(p.risks,'$[*] ? (@.risk == "high")')),
	jsonb_array_length(jsonb_path_query_array(p.risks,'$[*] ? (@.risk == "medium")')),
	jsonb_array_length(jsonb_path_query_array(p.risks,'$[*] ? (@.risk == "low")')),
	(` + reminderEligible + `)`

const today = `(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date`

const newestReport = `NOT EXISTS (
	SELECT 1 FROM business_cases newer WHERE newer.filing_id=r.filing_id
	AND newer.kind='report' AND newer.status='approved'
	AND (COALESCE(newer.submitted_at,newer.created_at),newer.created_at,newer.id) >
		(COALESCE(r.submitted_at,r.created_at),r.created_at,r.id))`

const filteredPlans = joins + `
	WHERE ($1='' OR strpos(lower(` + planFilingNumber + `),lower($1))>0 OR
		strpos(lower(r.number),lower($1))>0 OR strpos(lower(` + planSystemName + `),lower($1))>0 OR
		strpos(lower(COALESCE(p.frozen_organization_name,f.organization_name)),lower($1))>0)
	AND ($2='' OR p.status=$2)
	AND CASE $3::text
		WHEN 'overdue' THEN p.deadline < ` + today + `
		WHEN 'within_10_days' THEN p.deadline BETWEEN ` + today + ` AND ` + today + ` + 10
		WHEN 'within_30_days' THEN p.deadline BETWEEN ` + today + ` AND ` + today + ` + 30
		ELSE true END`

// scanFields 提供对外摘要的有序数据库扫描目标。
// 参数 item 为接收整改规划摘要的结果。
// 返回值 与公共查询投影对应的接收器列表。
func scanFields(item *summary) []any {
	return []any{&item.ID, &item.ReportID, &item.ReportNumber, &item.FilingNumber,
		&item.SystemName, &item.OrganizationName, &item.Deadline, &item.Status,
		&item.Submission, &item.SubmittedAt, &item.RiskCounts.High, &item.RiskCounts.Medium,
		&item.RiskCounts.Low, &item.ReminderEligible}
}

// get 读取指定整改规划关联信息和当前可用操作。
// 参数 ctx 含企业端可选范围，id 为整改规划标识。
// 返回值 详情，或未找到和持久化错误。
func (s *Service) get(ctx context.Context, id string) (detail, error) {
	var item detail
	var hasMaterials bool
	targets := append(scanFields(&item.summary), &item.Version, &item.Risks, &hasMaterials)
	err := s.db.QueryRow(ctx, "SELECT "+projection+`,p.version,p.risks,
		EXISTS(SELECT 1 FROM remediation_attachments WHERE plan_id=p.id) `+joins+`
		WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2)`, id, operatorOrganization(ctx)).Scan(targets...)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail{}, errNotFound
	}
	item.AllowedActions = allowedActions(item.Status, hasMaterials)
	if operatorOrganization(ctx) != "" && item.Status == pendingReview {
		item.AllowedActions = []string{"withdraw"}
	}
	return item, err
}

// list 按给定过滤条件查询整改规划，并保留有序翻页语义。
// 参数 ctx 为请求上下文，input 为合法状态、期限、关键词和分页位置。
// 返回值 一页公开记录，或读取错误。
func (s *Service) list(ctx context.Context, input listInput) (page, error) {
	rows, err := s.db.Query(ctx, "SELECT "+projection+",p.created_at "+filteredPlans+`
		AND ($4='' OR (p.created_at,p.id) < (NULLIF($4,'')::timestamptz,NULLIF($5,'')::uuid))
		AND ($7='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$7)
		ORDER BY p.created_at DESC,p.id DESC LIMIT $6`,
		input.Query, input.Status, input.Due, input.Key.At, input.Key.ID, input.Limit+1, operatorOrganization(ctx))
	if err != nil {
		return page{}, err
	}
	defer rows.Close()
	return scanPage(rows, input.Limit)
}

// scanPage 解码当前页并根据下一条记录推导稳定游标。
// 参数 rows 为数据库结果，limit 为可公开的单页条数。
// 返回值 可显示的记录和可用的下一页游标，或读取错误。
func scanPage(rows pgx.Rows, limit int) (page, error) {
	out := page{Items: []summary{}}
	var created time.Time
	for rows.Next() {
		if len(out.Items) == limit {
			out.NextCursor = makeCursor(created.UTC().Format(time.RFC3339Nano), out.Items[limit-1].ID)
			break
		}
		var item summary
		if err := rows.Scan(append(scanFields(&item), &created)...); err != nil {
			return page{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

// eligible 查询可关联报告，运营者在每份有效备案下仅可选当前最新通过报告。
// 参数 ctx 含可选单位范围，input 为检索词和数量上限。
// 返回值 最少必要的报告关联字段，或持久化错误。
func (s *Service) eligible(ctx context.Context, input listInput) (collection[eligibleReport], error) {
	rows, err := s.db.Query(ctx, `SELECT r.id::text,r.number,f.number,f.system_name,
		f.organization_name FROM business_cases r JOIN business_cases f ON f.id=r.filing_id
		WHERE r.kind='report' AND r.status='approved' AND f.kind='filing'
		AND f.status='approved' AND f.cancelled_at IS NULL
		AND ($3='' OR (f.organization_name=$3 AND `+newestReport+`))
		AND ($1='' OR strpos(lower(f.number),lower($1))>0 OR
			strpos(lower(r.number),lower($1))>0 OR strpos(lower(f.system_name),lower($1))>0 OR
			strpos(lower(f.organization_name),lower($1))>0)
		ORDER BY r.submitted_at DESC NULLS LAST,r.created_at DESC,r.id DESC LIMIT $2`,
		input.Query, input.Limit, operatorOrganization(ctx))
	if err != nil {
		return collection[eligibleReport]{}, err
	}
	defer rows.Close()
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[eligibleReport])
	return collection[eligibleReport]{Items: items}, err
}

// stats 返回当前计划状态、近期期限和最近首次审核结果的即时统计。
// 参数 ctx 为业务请求上下文。
// 返回值 整改规划统计，无首次审核样本时通过率为 null。
func (s *Service) stats(ctx context.Context) (statistics, error) {
	var out statistics
	err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM remediation_plans WHERE status='pending_review'),
		(SELECT count(*) FROM remediation_plans WHERE status='returned'),
		(SELECT count(*) FROM remediation_plans WHERE status NOT IN ('draft','withdrawn')
			AND deadline BETWEEN `+today+" AND "+today+` + 10),
		(SELECT round(100.0 * count(*) FILTER (WHERE action='approve') /
			NULLIF(count(*),0))::bigint FROM remediation_review_events
			WHERE submission=1 AND action IN ('approve','return_for_correction')
			AND occurred_at >= ((`+today+` - 89)::timestamp AT TIME ZONE 'Asia/Shanghai'))`).
		Scan(&out.Pending, &out.Returned, &out.DueWithin10Days, &out.FirstPassRate)
	return out, err
}

// history 加载一条整改规划的实际提交和审核事件。
// 参数 ctx 含可选运营单位范围，id 为整改规划标识。
// 返回值 按最新优先排列的事件，或记录不存在及数据库错误。
func (s *Service) history(ctx context.Context, id string) (collection[reviewEvent], error) {
	if err := s.ensurePlan(ctx, id); err != nil {
		return collection[reviewEvent]{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT e.action,e.from_status,e.to_status,e.operator,e.comment,e.reason,
		to_char(e.deadline,'YYYY-MM-DD'),e.submission,e.occurred_at `+joins+`
		JOIN remediation_review_events e ON e.plan_id=p.id
		WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2) ORDER BY e.id DESC`, id, operatorOrganization(ctx))
	if err != nil {
		return collection[reviewEvent]{}, err
	}
	defer rows.Close()
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[reviewEvent])
	return collection[reviewEvent]{Items: items}, err
}

// ensurePlan 核对指定整改规划存在。
// 参数 ctx 含企业端可选范围，id 为整改规划标识。
// 返回值 成功返回 nil，或记录不存在与读取错误。
func (s *Service) ensurePlan(ctx context.Context, id string) error {
	var present bool
	err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 "+joins+`
		WHERE p.id=$1 AND ($2='' OR COALESCE(p.frozen_organization_name,f.organization_name)=$2))`, id, operatorOrganization(ctx)).Scan(&present)
	if err == nil && !present {
		return errNotFound
	}
	return err
}
