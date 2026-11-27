ALTER TABLE business_cases ADD COLUMN cancelled_at TIMESTAMPTZ;
ALTER TABLE business_cases ADD CONSTRAINT business_cases_cancelled_filing
    CHECK (cancelled_at IS NULL OR (kind = 'filing' AND status = 'approved'));
CREATE INDEX business_cases_cancelled_created
    ON business_cases(created_at DESC, id DESC) WHERE cancelled_at IS NOT NULL;

CREATE TABLE filing_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filing_id UUID NOT NULL REFERENCES business_cases(id),
    application_type TEXT NOT NULL CHECK (application_type IN ('change', 'cancel')),
    filing_number TEXT NOT NULL,
    filing_version BIGINT NOT NULL,
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
    before_values JSONB NOT NULL,
    after_values JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_initial', 'pending_final', 'approved', 'returned')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    applied_at TIMESTAMPTZ,
    CHECK ((status = 'approved') = (applied_at IS NOT NULL)),
    CHECK ((application_type = 'cancel') = (before_values = after_values))
);
CREATE UNIQUE INDEX filing_applications_one_active
    ON filing_applications(filing_id) WHERE status <> 'approved';
CREATE INDEX filing_applications_created ON filing_applications(created_at DESC, id DESC);
CREATE INDEX filing_applications_by_filing
    ON filing_applications(filing_id, created_at DESC, id DESC);

CREATE TABLE filing_application_review_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    application_id UUID NOT NULL REFERENCES filing_applications(id),
    action TEXT NOT NULL CHECK (action IN ('submit','approve','return_for_correction')),
    from_status TEXT NOT NULL
        CHECK (from_status IN ('draft','pending_initial','pending_final','approved','returned')),
    to_status TEXT NOT NULL
        CHECK (to_status IN ('draft','pending_initial','pending_final','approved','returned')),
    operator TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(application_id, version)
);
CREATE INDEX filing_application_review_events_lookup
    ON filing_application_review_events(application_id, id DESC);
