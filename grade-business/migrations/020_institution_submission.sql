ALTER TABLE business_cases
    ADD COLUMN submission_profile text NOT NULL DEFAULT 'legacy',
    ADD CONSTRAINT business_cases_submission_profile_check
        CHECK (submission_profile = 'legacy' OR (kind = 'report' AND submission_profile = 'institution_v2'));

ALTER TABLE case_attachments
    DROP CONSTRAINT case_attachments_kind_check,
    ADD CONSTRAINT case_attachments_kind_check CHECK (kind IN (
        'filing_form', 'classification_report', 'report_file', 'supporting',
        'remediation_plan', 'assessment_conclusion', 'issues_recommendations', 'cover_basic_statement',
        'cloud_extension', 'big_data_extension', 'major_risk_recommendations'
    ));

CREATE INDEX business_cases_institution_created
    ON business_cases (kind, assessment_org, created_at DESC, id DESC);

CREATE INDEX filing_applications_institution_created
    ON filing_applications ((before_values->>'assessmentOrg'), created_at DESC, id DESC);
