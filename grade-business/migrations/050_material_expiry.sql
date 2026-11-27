CREATE TABLE material_expiry_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    request_id uuid NOT NULL UNIQUE,
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    filing_id uuid NOT NULL REFERENCES business_cases(id),
    case_id uuid NOT NULL REFERENCES business_cases(id),
    case_kind text NOT NULL CHECK (case_kind IN ('filing','report')),
    source_attachment_id uuid NOT NULL REFERENCES case_attachments(id),
    attachment_kind text NOT NULL CHECK (attachment_kind IN ('filing_form','classification_report',
        'report_file','assessment_conclusion','remediation_plan')),
    number text NOT NULL,
    system_name text NOT NULL,
    organization_name text NOT NULL CHECK (length(organization_name) BETWEEN 1 AND 200),
    institution_name text NOT NULL CHECK (length(institution_name)<=200),
    valid_until date NOT NULL,
    warning_days integer NOT NULL CHECK (warning_days BETWEEN 0 AND 90),
    evidence text NOT NULL CHECK (length(btrim(evidence)) BETWEEN 1 AND 1000),
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    cycle bigint NOT NULL DEFAULT 1 CHECK (cycle>0),
    retired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE UNIQUE INDEX material_expiry_one_per_type ON material_expiry_records(case_id,attachment_kind)
    WHERE retired_at IS NULL;
CREATE INDEX material_expiry_active_by_filing ON material_expiry_records(filing_id)
    WHERE retired_at IS NULL;
CREATE INDEX material_expiry_active_by_date ON material_expiry_records(valid_until,sequence)
    WHERE retired_at IS NULL;
CREATE INDEX material_expiry_organization ON material_expiry_records(organization_name,sequence DESC);
CREATE INDEX material_expiry_institution ON material_expiry_records(institution_name,sequence DESC);

CREATE TABLE material_expiry_renewals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    record_id uuid NOT NULL REFERENCES material_expiry_records(id),
    version bigint NOT NULL CHECK (version>1),
    audience text NOT NULL CHECK (audience IN ('operator','institution')),
    valid_until date NOT NULL,
    evidence text NOT NULL CHECK (length(btrim(evidence)) BETWEEN 1 AND 1000),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    file_name text NOT NULL CHECK (length(file_name) BETWEEN 1 AND 150),
    file_key text NOT NULL UNIQUE CHECK (file_key ~ '^[0-9a-f]{48}$'),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520),
    submitted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    decided_at timestamptz,
    UNIQUE(record_id,version),
    UNIQUE(record_id,id),
    CHECK ((status='pending') = (decided_at IS NULL))
);
CREATE UNIQUE INDEX material_expiry_one_pending ON material_expiry_renewals(record_id)
    WHERE status='pending';

CREATE TABLE material_expiry_events (
    record_id uuid NOT NULL REFERENCES material_expiry_records(id),
    version bigint NOT NULL CHECK (version>0),
    cycle bigint NOT NULL CHECK (cycle>0),
    action text NOT NULL CHECK (action IN ('register','submit','approve','reject','retire')),
    evidence text NOT NULL CHECK (length(evidence)<=1000),
    valid_until date NOT NULL,
    actor text NOT NULL,
    renewal_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (record_id,version),
    FOREIGN KEY (record_id,renewal_id) REFERENCES material_expiry_renewals(record_id,id),
    CHECK ((renewal_id IS NOT NULL) = (action IN ('submit','approve','reject')))
);

CREATE VIEW material_expiry_status AS
SELECT e.id,e.organization_name,e.institution_name,
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
    AND f.organization_name COLLATE "C"=e.organization_name
    AND c.assessment_org COLLATE "C"=e.institution_name AS current) scope;

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource,
    ADD CONSTRAINT review_notices_audience_resource CHECK (
        (audience='institution' AND resource IN ('filings','filing-applications','implementations',
            'hazards','alerts','material-issues','material-expiries'))
        OR (audience='institution' AND resource='reports' AND workflow_day IS NOT NULL)
        OR (audience='operator' AND resource IN ('filings','filing-applications','implementations',
            'remediations','hazards','alerts','material-issues','supervisions','material-expiries'))
    );
