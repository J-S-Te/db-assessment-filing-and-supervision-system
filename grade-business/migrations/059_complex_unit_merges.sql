-- 已归并的当前归属与原备案材料、审核可见范围分别保存。
ALTER TABLE business_cases
    ADD COLUMN merge_history_scope text,
    ADD COLUMN merge_history_number text,
    ADD COLUMN merge_history_system_name text;
CREATE INDEX business_cases_report_merge_history ON business_cases (merge_history_scope,filing_id)
    WHERE kind='report' AND merge_history_scope IS NOT NULL;
ALTER TABLE case_attachments ADD COLUMN merge_history_scope text;
ALTER TABLE case_review_events
    ADD COLUMN merge_history_scope text,
    ADD COLUMN merge_history_number text,
    ADD COLUMN merge_history_system_name text;
ALTER TABLE remediation_plans
    ADD COLUMN frozen_organization_name text,
    ADD COLUMN frozen_filing_number text,
    ADD COLUMN frozen_system_name text;
ALTER TABLE implementation_reports ADD COLUMN frozen_organization_name text;
ALTER TABLE hazard_items
    ADD COLUMN frozen_organization_name text,
    ADD COLUMN frozen_filing_number text,
    ADD COLUMN frozen_system_name text;
ALTER TABLE hazard_events ADD COLUMN merge_history_scope text;

CREATE OR REPLACE VIEW hazard_context AS
SELECT h.id, filing.id AS filing_id, report.id AS report_id,
       COALESCE(h.retest_institution,report.assessment_org,'') AS assessment_org,
       CASE WHEN h.registration_filing_id IS NOT NULL THEN h.retest_institution IS NOT NULL
            ELSE COALESCE(report.status='approved',false) END AS can_retest,
       COALESCE(h.frozen_system_name,filing.system_name) AS system_name,
       COALESCE(h.frozen_organization_name,filing.organization_name) AS organization_name,
       COALESCE(h.frozen_filing_number,filing.number) AS filing_number,
       COALESCE(report.number,'') AS report_number
FROM hazard_items h
LEFT JOIN remediation_plans p ON p.id=h.plan_id
LEFT JOIN business_cases report ON report.id=p.report_id AND report.kind='report'
JOIN business_cases filing ON filing.id=COALESCE(h.registration_filing_id,report.filing_id) AND filing.kind='filing';

ALTER TABLE material_expiry_records
    ADD COLUMN current_organization_name text,
    ADD COLUMN handoff_version bigint,
    ADD COLUMN handoff_valid_until date,
    ADD COLUMN handoff_cycle bigint,
    ADD COLUMN keep_retired_scope boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT material_expiry_handoff_snapshot CHECK (
      (current_organization_name IS NULL AND handoff_version IS NULL
       AND handoff_valid_until IS NULL AND handoff_cycle IS NULL)
      OR (current_organization_name IS NOT NULL AND handoff_version IS NOT NULL AND handoff_version > 1
          AND handoff_valid_until IS NOT NULL AND handoff_cycle IS NOT NULL AND handoff_cycle > 0)
    );

CREATE OR REPLACE VIEW material_expiry_status AS
SELECT e.id,COALESCE(e.current_organization_name,e.organization_name) AS organization_name,e.institution_name,
       scope.current AS scope_current,
       CASE WHEN e.retired_at IS NOT NULL THEN 'retired'
            WHEN NOT scope.current THEN 'suspended'
            WHEN e.valid_until<clock.today THEN 'expired'
            WHEN e.valid_until<=clock.today+e.warning_days THEN 'due'
            ELSE 'valid' END AS date_status
FROM material_expiry_records e
JOIN business_cases c ON c.id=e.case_id
JOIN business_cases f ON f.id=e.filing_id
CROSS JOIN LATERAL (SELECT (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date AS today) clock
CROSS JOIN LATERAL (SELECT c.status='approved' AND f.status='approved'
    AND c.discarded_at IS NULL AND f.discarded_at IS NULL AND f.cancelled_at IS NULL
    AND f.organization_name COLLATE "C"=COALESCE(e.current_organization_name,e.organization_name)
    AND c.assessment_org COLLATE "C"=e.institution_name AS current) scope;
