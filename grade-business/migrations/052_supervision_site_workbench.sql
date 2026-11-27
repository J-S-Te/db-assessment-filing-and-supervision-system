-- 监督详情、待办和定向期限提醒使用同一份当前备案归属判断。
CREATE VIEW supervision_filing_scope AS
SELECT t.id AS task_id,
       f.kind='filing' AND f.status='approved' AND f.cancelled_at IS NULL
           AND f.discarded_at IS NULL AND f.organization_name COLLATE "C"=t.organization_name
           AND btrim(f.organization_name)<>'' AS current_owner
FROM supervision_tasks t JOIN business_cases f ON f.id=t.filing_id;

CREATE INDEX supervision_site_active_follow_up ON supervision_site_inspections(task_id)
    WHERE status IN ('following_up','follow_up_submitted');

ALTER VIEW workflow_tasks RENAME TO workflow_with_annual_supervision;
CREATE VIEW workflow_tasks AS
SELECT id,resource,label,system_name,number,status,organization,institution,applicant_audience,
       audience,stage_at,stage_version,window_kind,explicit_deadline,recipient
FROM workflow_with_annual_supervision
UNION ALL
SELECT t.id,'supervision-site',
       CASE WHEN s.status='following_up' THEN '现场检查跟进' ELSE '现场检查复核' END,
       t.system_name,t.filing_number,s.status,t.organization_name,''::text,'operator',
       CASE WHEN s.status='following_up' THEN 'operator' ELSE 'regulator' END,
       e.occurred_at,e.version,''::text,
       CASE WHEN s.status='following_up' THEN s.follow_up_due_on END,
       CASE WHEN s.status='following_up' THEN t.organization_name ELSE '' END
FROM supervision_site_inspections s
JOIN supervision_tasks t ON t.id=s.task_id
JOIN supervision_filing_scope f ON f.task_id=t.id
JOIN LATERAL (
    SELECT occurred_at,version FROM supervision_site_events
    WHERE task_id=s.task_id AND status=s.status AND version<=s.version
      AND action IN ('follow_up','return_follow_up','submit_follow_up')
    ORDER BY version DESC LIMIT 1
) e ON true
WHERE s.status='follow_up_submitted' OR (
    s.status='following_up' AND t.published_at IS NOT NULL AND t.status<>'cancelled'
    AND f.current_owner);

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations',
            'hazards','alerts','material-issues','material-expiries'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts','material-issues','supervisions','material-expiries'))
        OR (audience='operator' AND resource='supervision-site' AND workflow_day IS NOT NULL)
    );
