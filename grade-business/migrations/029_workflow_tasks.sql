CREATE TABLE workflow_calendar_days (
    day date PRIMARY KEY,
    is_workday boolean NOT NULL
);

-- 从进入审核阶段的下一日开始，工作日覆盖可区分补班和休息。
CREATE FUNCTION workflow_due(start_day date, days integer) RETURNS date
    LANGUAGE plpgsql STABLE STRICT AS $$
DECLARE
    current_day date := start_day;
    remaining integer := days;
BEGIN
    IF remaining <= 0 THEN
        RETURN NULL;
    END IF;
    WHILE remaining > 0 LOOP
        current_day := current_day + 1;
        IF COALESCE(
            (SELECT is_workday FROM workflow_calendar_days WHERE day = current_day),
            EXTRACT(ISODOW FROM current_day) BETWEEN 1 AND 5
        ) THEN
            remaining := remaining - 1;
        END IF;
    END LOOP;
    RETURN current_day;
END;
$$;

-- 一个记录只投影给当前办理方，归属及时间来自当前业务记录和对应流转。
CREATE VIEW workflow_tasks AS
WITH tasks AS (
    SELECT c.id, c.kind || 's' AS resource,
           (CASE c.kind WHEN 'filing' THEN '备案' ELSE '测评报告' END) ||
           (CASE c.status WHEN 'pending_initial' THEN '初审' WHEN 'pending_final' THEN '终审' ELSE '补正' END) AS label,
           COALESCE(f.system_name,c.system_name) AS system_name, c.number, c.status,
           COALESCE(f.organization_name,c.organization_name) AS organization,
           c.assessment_org AS institution,
           CASE WHEN c.operator_origin THEN 'operator' ELSE 'institution' END AS applicant_audience,
           CASE WHEN c.status = 'returned' AND c.operator_origin THEN 'operator'
                WHEN c.status = 'returned' OR (c.status = 'pending_initial' AND c.operator_origin) THEN 'institution'
                ELSE 'regulator' END AS audience,
           COALESCE(stage.occurred_at,c.submitted_at,c.created_at) AS stage_at,
           COALESCE(stage.version,c.version) AS stage_version,
           CASE WHEN c.status = 'returned' THEN '' WHEN c.kind = 'report' THEN 'report'
                WHEN c.status = 'pending_initial' THEN 'filing_initial' ELSE 'filing_final' END AS window_kind,
           NULL::date AS explicit_deadline
    FROM business_cases c LEFT JOIN business_cases f ON f.id=c.filing_id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM case_review_events e
        WHERE e.case_id=c.id AND e.to_status=c.status ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE c.status IN ('pending_initial','pending_final','returned')

    UNION ALL

    SELECT a.id,'filing-applications',
           (CASE a.application_type WHEN 'cancel' THEN '备案注销' ELSE '备案变更' END) ||
           (CASE a.status WHEN 'pending_initial' THEN '初审' WHEN 'pending_final' THEN '终审' ELSE '补正' END),
           f.system_name,a.filing_number,a.status,
           COALESCE(a.before_values->>'organizationName',''),COALESCE(a.before_values->>'assessmentOrg',''),
           CASE WHEN a.operator_origin THEN 'operator' ELSE 'institution' END,
           CASE WHEN a.status = 'returned' AND a.operator_origin THEN 'operator'
                WHEN a.status = 'returned' OR (a.status='pending_initial' AND a.operator_origin) THEN 'institution'
                ELSE 'regulator' END,
           COALESCE(stage.occurred_at,a.created_at),COALESCE(stage.version,a.version),
           CASE a.status WHEN 'pending_initial' THEN 'filing_initial' WHEN 'pending_final' THEN 'filing_final' ELSE '' END,
           NULL::date
    FROM filing_applications a JOIN business_cases f ON f.id=a.filing_id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM filing_application_review_events e
        WHERE e.application_id=a.id AND e.to_status=a.status
        ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE a.status IN ('pending_initial','pending_final','returned')

    UNION ALL

    SELECT i.id,'implementations',
           '实施报备' || CASE i.status WHEN 'submitted' THEN '受理' WHEN 'pending_initial' THEN '初审'
                         WHEN 'pending_final' THEN '终审' WHEN 'pending_supervision' THEN '复核' ELSE '补正' END,
           i.system_name,i.filing_number,i.status,f.organization_name,i.assessment_org,
           CASE WHEN i.created_by='本地运营者：'||f.organization_name THEN 'operator'
                WHEN starts_with(i.created_by,'本地运营者：') THEN '' ELSE 'institution' END,
           CASE WHEN i.status <> 'returned' THEN 'regulator'
                WHEN i.created_by='本地运营者：'||f.organization_name THEN 'operator'
                WHEN starts_with(i.created_by,'本地运营者：') THEN 'regulator' ELSE 'institution' END,
           COALESCE(stage.occurred_at,i.submitted_at,i.created_at),COALESCE(stage.version,i.version),
           CASE WHEN i.status='returned' THEN '' ELSE 'implementation' END,NULL::date
    FROM implementation_reports i JOIN business_cases f ON f.id=i.filing_id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM implementation_events e
        WHERE e.report_id=i.id AND e.to_status=i.status AND e.from_status<>e.to_status
        ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE i.status IN ('submitted','pending_initial','pending_final','pending_supervision','returned')

    UNION ALL

    SELECT p.id,'remediations',
           CASE p.status WHEN 'pending_review' THEN '整改规划审核' ELSE '整改规划补正' END,
           f.system_name,r.number,p.status,f.organization_name,r.assessment_org,'operator',
           CASE p.status WHEN 'pending_review' THEN 'regulator' ELSE 'operator' END,
           COALESCE(stage.occurred_at,p.submitted_at,p.created_at),COALESCE(stage.version,p.version),
           CASE p.status WHEN 'pending_review' THEN 'remediation' ELSE '' END,
           CASE p.status WHEN 'returned' THEN p.deadline END
    FROM remediation_plans p JOIN business_cases r ON r.id=p.report_id
    JOIN business_cases f ON f.id=r.filing_id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM remediation_review_events e
        WHERE e.plan_id=p.id AND e.to_status=p.status ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE p.status IN ('pending_review','returned')

    UNION ALL

    SELECT h.id,'hazards',
           CASE h.status WHEN 'pending_retest' THEN '隐患复测' WHEN 'pending_review' THEN '隐患复核'
                         WHEN 'returned' THEN '隐患补正' ELSE '隐患整改' END,
           f.system_name,f.number,h.status,f.organization_name,r.assessment_org,'operator',
           CASE h.status WHEN 'pending_retest' THEN 'institution'
                         WHEN 'pending_review' THEN 'regulator' ELSE 'operator' END,
           COALESCE(stage.occurred_at,h.created_at),COALESCE(stage.version,1),'',h.deadline
    FROM hazard_items h JOIN remediation_plans p ON p.id=h.plan_id
    JOIN business_cases r ON r.id=p.report_id JOIN business_cases f ON f.id=r.filing_id
    LEFT JOIN LATERAL (
        SELECT e.version,e.occurred_at FROM hazard_events e
        WHERE e.hazard_id=h.id AND e.to_status=h.status AND e.from_status<>e.to_status
        ORDER BY e.version DESC LIMIT 1
    ) stage ON true
    WHERE h.status <> 'closed' AND (h.status<>'pending_retest' OR r.status='approved')

    UNION ALL

    SELECT a.id,'alerts',
           CASE WHEN d.id IS NOT NULL THEN
                CASE d.action WHEN 'request_extension' THEN '延期审批' ELSE '误报审批' END
                ELSE '预警处置' END,
           f.system_name,f.number,a.status,f.organization_name,r.assessment_org,'operator',
           CASE WHEN d.id IS NOT NULL THEN 'regulator' ELSE 'operator' END,
           COALESCE(d.requested_at,a.triggered_at),COALESCE(d.requested_version,a.version),'',NULL::date
    FROM local_alerts a JOIN hazard_items h ON h.id=a.hazard_id
    JOIN remediation_plans p ON p.id=h.plan_id JOIN business_cases r ON r.id=p.report_id
    JOIN business_cases f ON f.id=r.filing_id
    LEFT JOIN local_alert_dispositions d ON d.hazard_id=h.id AND d.status='pending'
    WHERE a.status <> 'closed' AND (d.id IS NOT NULL OR h.status<>'closed')
      AND (d.id IS NULL OR d.alert_id=a.id)
)
SELECT tasks.*, CASE audience WHEN 'operator' THEN organization
                             WHEN 'institution' THEN institution ELSE '' END AS recipient
FROM tasks;
