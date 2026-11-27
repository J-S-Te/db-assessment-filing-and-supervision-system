ALTER TABLE business_cases ADD COLUMN discarded_at timestamptz,
    ADD CONSTRAINT business_cases_discarded_draft CHECK (
        discarded_at IS NULL OR (kind='report' AND status='draft' AND submitted_at IS NULL)
    );
DROP INDEX business_cases_kind_number;
CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind,lower(number))
    WHERE discarded_at IS NULL;

ALTER TABLE case_attachments ADD COLUMN upload_digest text,
    DROP CONSTRAINT case_attachments_kind_check,
    ADD CONSTRAINT case_attachments_kind_check CHECK (kind IN (
        'filing_form','classification_report','filing_package','report_file','supporting',
        'remediation_plan','assessment_conclusion','issues_recommendations','cover_basic_statement',
        'cloud_extension','big_data_extension','major_risk_recommendations'
    ));

CREATE TABLE material_issues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    case_id uuid NOT NULL REFERENCES business_cases(id),
    case_kind text NOT NULL CHECK (case_kind IN ('filing','report')),
    institution text NOT NULL,
    organization_name text NOT NULL,
    system_name text NOT NULL,
    reference text NOT NULL,
    material_kind text NOT NULL,
    file_name text NOT NULL,
    reason_code text NOT NULL,
    reason text NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    closed_at timestamptz,
    resolution text,
    resolved_attachment_id uuid REFERENCES case_attachments(id),
    CHECK ((closed_at IS NULL AND resolution IS NULL AND resolved_attachment_id IS NULL)
        OR (closed_at IS NOT NULL AND (
            (resolution IS NOT DISTINCT FROM 'corrected' AND resolved_attachment_id IS NOT NULL)
            OR (resolution IS NOT DISTINCT FROM 'discarded' AND resolved_attachment_id IS NULL)
        )))
);
CREATE UNIQUE INDEX material_issues_one_open ON material_issues(case_id,material_kind)
    WHERE closed_at IS NULL;
CREATE INDEX material_issues_institution ON material_issues(institution,sequence DESC);
CREATE INDEX material_issues_organization ON material_issues(organization_name,sequence DESC);
CREATE INDEX material_issues_case ON material_issues(case_id);

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations',
            'hazards','alerts','material-issues'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts','material-issues'))
    );
