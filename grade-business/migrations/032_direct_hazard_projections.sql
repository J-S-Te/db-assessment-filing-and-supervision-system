-- 保留其他业务的待办，隐患和预警改用同时覆盖登记与规划来源的备案归属。
ALTER VIEW workflow_tasks RENAME TO workflow_plan_tasks;

CREATE VIEW workflow_tasks AS
WITH hazard_tasks AS (
    SELECT h.id,'hazards'::text AS resource,
           CASE h.status WHEN 'pending_dispatch' THEN '隐患派发' WHEN 'pending_retest' THEN '隐患复测'
                WHEN 'pending_review' THEN '隐患复核' WHEN 'returned' THEN '隐患补正' ELSE '隐患整改' END AS label,
           hc.system_name,hc.filing_number AS number,h.status,hc.organization_name AS organization,
           hc.assessment_org AS institution,'operator'::text AS applicant_audience,
           CASE WHEN h.status='pending_retest' THEN 'institution'
                WHEN h.status IN ('pending_dispatch','pending_review') THEN 'regulator' ELSE 'operator' END AS audience,
           COALESCE(stage.occurred_at,h.dispatched_at,h.created_at) AS stage_at,
           COALESCE(stage.version,1) AS stage_version,''::text AS window_kind,
           CASE WHEN h.status<>'pending_dispatch' THEN h.deadline END AS explicit_deadline
    FROM hazard_items h JOIN hazard_context hc ON hc.id=h.id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM hazard_events e
        WHERE e.hazard_id=h.id AND e.to_status=h.status AND e.from_status<>e.to_status
        ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE h.status<>'closed' AND (h.status<>'pending_retest' OR hc.can_retest)

    UNION ALL

    SELECT a.id,'alerts',
           CASE WHEN d.id IS NOT NULL THEN
                CASE d.action WHEN 'request_extension' THEN '延期审批' ELSE '误报审批' END
                ELSE '预警处置' END,
           hc.system_name,hc.filing_number,a.status,hc.organization_name,hc.assessment_org,'operator',
           CASE WHEN d.id IS NOT NULL THEN 'regulator' ELSE 'operator' END,
           COALESCE(d.requested_at,a.triggered_at),COALESCE(d.requested_version,a.version),'',NULL::date
    FROM local_alerts a JOIN hazard_items h ON h.id=a.hazard_id JOIN hazard_context hc ON hc.id=h.id
    LEFT JOIN local_alert_dispositions d ON d.hazard_id=h.id AND d.status='pending'
    WHERE a.status<>'closed' AND h.status<>'pending_dispatch'
      AND (d.id IS NOT NULL OR h.status<>'closed') AND (d.id IS NULL OR d.alert_id=a.id)
)
SELECT id,resource,label,system_name,number,status,organization,institution,applicant_audience,audience,
       stage_at,stage_version,window_kind,explicit_deadline,recipient
FROM workflow_plan_tasks WHERE resource NOT IN ('hazards','alerts')
UNION ALL
SELECT hazard_tasks.*, CASE audience WHEN 'operator' THEN organization
                                    WHEN 'institution' THEN institution ELSE '' END AS recipient
FROM hazard_tasks;
