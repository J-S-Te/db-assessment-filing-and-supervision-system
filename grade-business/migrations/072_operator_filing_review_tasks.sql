-- 已登录运营者提交的首报由监管初审，工作台与业务时限使用同一办理方。
ALTER VIEW workflow_tasks RENAME TO workflow_before_operator_filing_review;

CREATE VIEW workflow_tasks AS
SELECT w.id,w.resource,w.label,w.system_name,w.number,w.status,w.organization,w.institution,
       w.applicant_audience,
       CASE WHEN e.case_id IS NOT NULL THEN 'regulator' ELSE w.audience END AS audience,
       w.stage_at,w.stage_version,w.window_kind,w.explicit_deadline,
       CASE WHEN e.case_id IS NOT NULL THEN '' ELSE w.recipient END AS recipient
FROM workflow_before_operator_filing_review w
LEFT JOIN case_review_events e ON w.resource='filings' AND w.status='pending_initial'
    AND w.audience='institution' AND e.case_id=w.id AND e.version=w.stage_version
    AND e.action='submit' AND e.operator_role IS NOT NULL;
