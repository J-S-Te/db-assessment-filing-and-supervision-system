ALTER TABLE filing_applications
    ADD COLUMN operator_origin boolean NOT NULL DEFAULT false,
    ADD COLUMN implementation_id uuid REFERENCES implementation_reports(id),
    ADD CONSTRAINT filing_application_operator_organization CHECK (
        NOT operator_origin OR before_values->>'organizationName' IS NOT DISTINCT FROM after_values->>'organizationName'
    );

ALTER TABLE filing_application_review_events
    ADD COLUMN implementation_id uuid REFERENCES implementation_reports(id);

-- 复测依据只在换机构申请终审生效时消费，退回与撤回保留历史而不消耗。
CREATE UNIQUE INDEX filing_applications_implementation_once ON filing_applications(implementation_id)
    WHERE status='approved' AND implementation_id IS NOT NULL;

CREATE INDEX approved_implementation_retest_lookup
    ON implementation_reports(filing_id, assessment_org, accepted_at DESC, created_at DESC, id DESC)
    WHERE report_type='change' AND status='approved';

CREATE TABLE filing_application_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id uuid NOT NULL REFERENCES filing_applications(id),
    kind text NOT NULL CHECK (kind IN ('change_statement','cancellation_statement','supporting')),
    file_name text NOT NULL,
    file_key text NOT NULL UNIQUE,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    uploaded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX filing_application_material_lookup
    ON filing_application_attachments(application_id, uploaded_at DESC, id DESC);
