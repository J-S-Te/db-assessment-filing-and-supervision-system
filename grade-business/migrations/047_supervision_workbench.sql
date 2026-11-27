-- 自查报送人和监管审核人按同一状态进入待办。上传材料不改变通知使用的阶段版本。
ALTER VIEW workflow_tasks RENAME TO workflow_case_review_tasks;

CREATE VIEW workflow_tasks AS
SELECT id,resource,label,system_name,number,status,organization,institution,applicant_audience,
       audience,stage_at,stage_version,window_kind,explicit_deadline,recipient
FROM workflow_case_review_tasks
UNION ALL
SELECT t.id,'supervisions',
       CASE t.status WHEN 'submitted' THEN '自查报告审核'
                     WHEN 'returned' THEN '监督检查补正' ELSE '监督检查自查' END,
       t.system_name,t.filing_number,t.status,t.organization_name,''::text,'operator',
       CASE WHEN t.status='submitted' THEN 'regulator' ELSE 'operator' END,
       COALESCE(e.created_at,t.created_at),COALESCE(e.version,1::bigint),''::text,
       CASE WHEN t.status IN ('open','returned') THEN t.due_on END,
       CASE WHEN t.status='submitted' THEN '' ELSE t.organization_name END
FROM supervision_tasks t JOIN business_cases f ON f.id=t.filing_id
LEFT JOIN LATERAL (
    SELECT version,created_at FROM supervision_events
    WHERE task_id=t.id AND to_status=t.status AND action IN ('publish','return','submit')
    ORDER BY version DESC LIMIT 1
) e ON true
WHERE t.status='submitted' OR (
    t.status IN ('open','returned') AND f.status='approved' AND f.cancelled_at IS NULL
    AND f.organization_name COLLATE "C"=t.organization_name);
