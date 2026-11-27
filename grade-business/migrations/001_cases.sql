CREATE TABLE business_cases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL CHECK (kind IN ('filing', 'report')),
    number TEXT NOT NULL,
    system_name TEXT NOT NULL DEFAULT '',
    organization_name TEXT NOT NULL DEFAULT '',
    assessment_org TEXT NOT NULL,
    level TEXT NOT NULL DEFAULT '',
    filing_id UUID REFERENCES business_cases(id),
    risk TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_initial', 'pending_final', 'approved', 'returned')),
    version BIGINT NOT NULL DEFAULT 1,
    material_revision BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    submitted_at TIMESTAMPTZ,
    CHECK (
        (kind = 'filing' AND filing_id IS NULL AND level IN ('1','2','3','4','5') AND risk = '')
        OR
        (kind = 'report' AND filing_id IS NOT NULL AND level = '' AND risk IN ('low','medium','high'))
    )
);
CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind, lower(number));
CREATE INDEX business_cases_kind_created ON business_cases(kind, created_at DESC, id DESC);
CREATE INDEX business_cases_kind_status_created
    ON business_cases(kind, status, created_at DESC, id DESC);
CREATE INDEX business_cases_by_filing ON business_cases(filing_id) WHERE filing_id IS NOT NULL;

CREATE TABLE case_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id UUID NOT NULL REFERENCES business_cases(id),
    kind TEXT NOT NULL CHECK (kind IN ('filing_form','classification_report','report_file','supporting')),
    file_name TEXT NOT NULL,
    file_key TEXT NOT NULL UNIQUE,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX case_attachments_case_upload ON case_attachments(case_id, uploaded_at, id);

CREATE TABLE case_review_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    case_id UUID NOT NULL REFERENCES business_cases(id),
    action TEXT NOT NULL CHECK (action IN ('submit','approve','return_for_correction')),
    from_status TEXT NOT NULL
        CHECK (from_status IN ('draft','pending_initial','pending_final','approved','returned')),
    to_status TEXT NOT NULL
        CHECK (to_status IN ('draft','pending_initial','pending_final','approved','returned')),
    operator TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(case_id, version)
);
