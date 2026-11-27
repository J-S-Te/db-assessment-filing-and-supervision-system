-- 两侧可协同处理已分派的预警，提交反馈后由监管工作台独占审批。
ALTER VIEW workflow_tasks RENAME TO workflow_hazard_tasks;

CREATE VIEW workflow_tasks AS
SELECT t.id,t.resource,
       CASE WHEN d.id IS NOT NULL THEN '机构预警反馈审批' ELSE t.label END AS label,
       t.system_name,t.number,t.status,t.organization,t.institution,
       COALESCE(d.requester_audience,t.applicant_audience) AS applicant_audience,
       t.audience,t.stage_at,t.stage_version,t.window_kind,t.explicit_deadline,t.recipient
FROM workflow_hazard_tasks t
LEFT JOIN local_alert_dispositions d ON t.resource='alerts' AND t.audience='regulator'
  AND d.alert_id=t.id AND d.action='submit_feedback' AND d.status='pending'
UNION ALL
SELECT a.id,'alerts',
       CASE a.status WHEN 'pending' THEN '机构预警处理' ELSE '机构预警反馈' END,
       hc.system_name,hc.filing_number,a.status,hc.organization_name,hc.assessment_org,
       'institution','institution',COALESCE(e.occurred_at,a.triggered_at),a.version,
       '',NULL::date,hc.assessment_org
FROM local_alerts a JOIN hazard_items h ON h.id=a.hazard_id
JOIN hazard_context hc ON hc.id=h.id
LEFT JOIN local_alert_events e ON e.alert_id=a.id AND e.version=a.version
WHERE a.status IN ('pending','processing') AND h.status<>'pending_dispatch'
  AND hc.can_retest AND btrim(hc.assessment_org)<>''
  AND NOT EXISTS (SELECT 1 FROM local_alert_dispositions d
                  WHERE d.hazard_id=h.id AND d.status='pending');
