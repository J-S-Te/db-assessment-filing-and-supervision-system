CREATE TABLE assessment_due_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    request_id uuid UNIQUE NOT NULL,
    request_digest text NOT NULL,
    filing_id uuid NOT NULL REFERENCES business_cases(id),
    due_on date NOT NULL,
    warning_days integer NOT NULL CHECK (warning_days BETWEEN 0 AND 90),
    evidence text NOT NULL CHECK (char_length(btrim(evidence)) BETWEEN 1 AND 1000),
    version bigint NOT NULL DEFAULT 1,
    cycle bigint NOT NULL DEFAULT 1,
    closed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE UNIQUE INDEX assessment_due_active_filing ON assessment_due_records (filing_id)
    WHERE closed_at IS NULL;
CREATE INDEX assessment_due_active_date ON assessment_due_records (due_on, sequence)
    WHERE closed_at IS NULL;

CREATE TABLE assessment_due_events (
    record_id uuid NOT NULL REFERENCES assessment_due_records(id),
    version bigint NOT NULL,
    cycle bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('register', 'revise', 'close')),
    due_on date NOT NULL,
    warning_days integer NOT NULL,
    evidence text NOT NULL,
    actor text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (record_id, version)
);

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations',
            'hazards','alerts','material-issues','material-expiries'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts','material-issues','supervisions','material-expiries'))
        OR (audience='operator' AND resource='supervision-site' AND workflow_day IS NOT NULL)
        OR (audience IN ('operator', 'institution') AND resource='assessment-dues'
            AND state IN ('assessment_due_soon', 'assessment_due')
            AND workflow_day IS NULL AND reminder_day IS NULL)
    );

DROP INDEX review_notices_event_once;
CREATE UNIQUE INDEX review_notices_event_once
    ON review_notices (audience, resource, resource_id, event_version, recipient)
    WHERE reminder_day IS NULL AND workflow_day IS NULL
      AND NOT (resource = 'material-expiries' AND state IN ('material_due', 'material_expired'))
      AND NOT (resource = 'assessment-dues' AND state IN ('assessment_due_soon', 'assessment_due'));

CREATE UNIQUE INDEX review_notices_assessment_date_once
    ON review_notices (audience, resource, resource_id, event_version, recipient, state)
    WHERE resource = 'assessment-dues' AND state IN ('assessment_due_soon', 'assessment_due');
