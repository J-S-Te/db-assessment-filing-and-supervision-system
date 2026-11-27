CREATE TABLE supervision_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    request_id uuid NOT NULL UNIQUE,
    filing_id uuid NOT NULL REFERENCES business_cases(id),
    lead_alert_id uuid REFERENCES alert_supervision_leads(alert_id),
    filing_number text NOT NULL,
    system_name text NOT NULL,
    organization_name text NOT NULL CHECK (length(organization_name) BETWEEN 1 AND 200),
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    due_on date NOT NULL,
    cycle_year integer NOT NULL CHECK (cycle_year=extract(year FROM due_on)),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft','open','submitted','returned','approved','cancelled')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_at timestamptz,
    submitted_at timestamptz,
    submitted_version bigint,
    return_version bigint,
    correction_kind text CHECK (correction_kind IN ('inspection_form','rectification_summary','both')),
    CHECK ((submitted_at IS NULL) = (submitted_version IS NULL)),
    CHECK ((return_version IS NULL) = (correction_kind IS NULL)),
    CHECK (status IN ('draft','cancelled') OR published_at IS NOT NULL),
    CHECK (status NOT IN ('submitted','returned','approved') OR submitted_version IS NOT NULL),
    CHECK (status <> 'returned' OR return_version IS NOT NULL)
);

CREATE UNIQUE INDEX supervision_annual_once ON supervision_tasks(filing_id, cycle_year)
    WHERE lead_alert_id IS NULL AND status<>'cancelled';
CREATE UNIQUE INDEX supervision_lead_once ON supervision_tasks(lead_alert_id)
    WHERE lead_alert_id IS NOT NULL AND status<>'cancelled';
CREATE INDEX supervision_operator_queue ON supervision_tasks(organization_name, sequence DESC)
    WHERE published_at IS NOT NULL;
CREATE INDEX supervision_filing_tasks ON supervision_tasks(filing_id);

CREATE TABLE supervision_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES supervision_tasks(id),
    kind text NOT NULL CHECK (kind IN (
        'inspection_template','summary_template','inspection_form','rectification_summary'
    )),
    file_name text NOT NULL CHECK (length(file_name) BETWEEN 1 AND 150),
    file_key text NOT NULL UNIQUE CHECK (file_key ~ '^[0-9a-f]{48}$'),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520),
    task_version bigint NOT NULL CHECK (task_version > 1),
    uploaded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (task_id, id),
    UNIQUE (task_id, task_version)
);
CREATE INDEX supervision_attachments_latest ON supervision_attachments(task_id, kind, task_version DESC);

CREATE TABLE supervision_events (
    task_id uuid NOT NULL REFERENCES supervision_tasks(id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('create','publish','submit','return','approve','cancel')),
    from_status text,
    to_status text NOT NULL,
    actor text NOT NULL,
    comment text NOT NULL DEFAULT '',
    correction_kind text,
    inspection_file_id uuid,
    summary_file_id uuid,
    rectification jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (task_id, version),
    FOREIGN KEY (task_id, inspection_file_id) REFERENCES supervision_attachments(task_id, id),
    FOREIGN KEY (task_id, summary_file_id) REFERENCES supervision_attachments(task_id, id),
    CHECK ((inspection_file_id IS NOT NULL) = (action='submit')),
    CHECK ((summary_file_id IS NOT NULL) = (action='submit')),
    CHECK ((rectification IS NOT NULL) = (action='submit'))
);

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations',
            'hazards','alerts','material-issues'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts','material-issues','supervisions'))
    );
