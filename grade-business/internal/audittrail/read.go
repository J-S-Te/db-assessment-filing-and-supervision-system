package audittrail

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

const recordedOperations = `
WITH operations (event_key, resource, resource_id, title, reference, action, stage, operator, occurred_at) AS (
	SELECT 'case:'||e.id::text, CASE c.kind WHEN 'filing' THEN 'filings' ELSE 'reports' END,c.id,
		COALESCE(NULLIF(f.system_name,''),NULLIF(c.system_name,''),c.number),c.number,
		e.action,e.from_status,e.operator,e.occurred_at
	FROM case_review_events e JOIN business_cases c ON c.id=e.case_id
	LEFT JOIN business_cases f ON f.id=c.filing_id
	UNION ALL
	SELECT 'filing-application:'||e.id::text,'filing-applications',a.id,
		COALESCE(NULLIF(a.before_values->>'systemName',''),NULLIF(f.system_name,''),a.filing_number),a.filing_number,
		e.action,e.from_status,e.operator,e.occurred_at
	FROM filing_application_review_events e JOIN filing_applications a ON a.id=e.application_id
	JOIN business_cases f ON f.id=a.filing_id
	UNION ALL
	SELECT 'implementation:'||e.report_id::text||':'||e.version::text,'implementations',r.id,
		r.system_name,r.filing_number,e.action,e.from_status,e.operator,e.occurred_at
	FROM implementation_events e JOIN implementation_reports r ON r.id=e.report_id
	UNION ALL
	SELECT 'remediation:'||e.id::text,'remediations',p.id,COALESCE(NULLIF(f.system_name,''),r.number),
		r.number,e.action,e.from_status,e.operator,e.occurred_at
	FROM remediation_review_events e JOIN remediation_plans p ON p.id=e.plan_id
	JOIN business_cases r ON r.id=p.report_id JOIN business_cases f ON f.id=r.filing_id
	UNION ALL
	SELECT 'hazard:'||e.id::text,'hazards',hc.id,
		COALESCE(NULLIF(hc.system_name,''),NULLIF(hc.report_number,''),hc.filing_number),
		COALESCE(NULLIF(hc.report_number,''),hc.filing_number),e.action,e.from_status,e.operator,e.occurred_at
	FROM hazard_events e JOIN hazard_context hc ON hc.id=e.hazard_id
	UNION ALL
	SELECT 'alert:'||e.alert_id::text||':'||e.version::text,'alerts',a.id,
		a.system_name,'',e.action,COALESCE(e.from_status,''),e.operator,e.occurred_at
	FROM local_alert_events e JOIN local_alerts a ON a.id=e.alert_id
	UNION ALL
	SELECT 'publication:'||e.sequence::text,'publications',e.publication_id,
		e.title,'',e.action,'',e.operator,e.occurred_at
	FROM publication_events e
	UNION ALL
	SELECT 'report-extraction:'||e.attachment_id::text||':'||e.version::text,'reports',r.id,
		COALESCE(NULLIF(f.system_name,''),r.number),r.number,'confirm_extraction','',e.operator,e.confirmed_at
	FROM report_extraction_reviews e JOIN case_attachments a ON a.id=e.attachment_id
	JOIN business_cases r ON r.id=a.case_id AND r.kind='report' JOIN business_cases f ON f.id=r.filing_id
	UNION ALL
	SELECT 'report-compliance:'||e.sequence::text,'reports',r.id,
		COALESCE(NULLIF(f.system_name,''),r.number),r.number,'record_compliance','',e.operator,e.created_at
	FROM report_compliance_records e JOIN business_cases r ON r.id=e.report_id AND r.kind='report'
	JOIN business_cases f ON f.id=r.filing_id
	UNION ALL
	SELECT 'workday-calendar:'||e.request_id::text,'workday-calendar',e.request_id,
		'工作日历',to_char(e.day,'YYYY-MM-DD'),e.mode,'',e.operator,e.changed_at
	FROM workflow_calendar_events e
)
SELECT event_key,resource,resource_id::text,title,reference,action,stage,operator,occurred_at
FROM operations
WHERE ($1::text='' OR resource=$1)
  AND ($2::text='' OR strpos(lower(concat_ws(' ',operator,title,reference)),lower($2))>0)
  AND ($3::timestamptz IS NULL OR (occurred_at,event_key COLLATE "C")<($3,$4::text))
ORDER BY occurred_at DESC,event_key COLLATE "C" DESC LIMIT $5
`

// list 在单次数据库读取中合并各模块的业务留痕。
// 参数 ctx 控制查询，filters 为已验证的业务类型、关键词和分页位置。
// 返回值 已发生操作的一页摘要和可选的下一页游标。
func (s *Service) list(ctx context.Context, filters auditFilter) (auditPage, error) {
	at := pgtype.Timestamptz{Time: filters.cursor.at, Valid: filters.cursor.key != ""}
	rows, err := s.db.Query(ctx, recordedOperations, filters.resource, filters.query, at, filters.cursor.key, filters.limit+1)
	if err != nil {
		return auditPage{}, err
	}
	defer rows.Close()

	result := auditPage{Items: make([]auditItem, 0, filters.limit)}
	for rows.Next() {
		if len(result.Items) == filters.limit {
			result.NextCursor = encodeCursor(result.Items[len(result.Items)-1])
			break
		}
		var item auditItem
		var stage string
		err := rows.Scan(&item.ID, &item.Resource, &item.ResourceID, &item.Title, &item.Reference,
			&item.Action, &stage, &item.Operator, &item.OccurredAt)
		if err != nil {
			return auditPage{}, err
		}
		item.ActionLabel = actionLabel(item.Resource, item.Action, stage)
		item.Href = businessLink(item.Resource, item.ResourceID, item.Reference)
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}
