CREATE TABLE implementation_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filing_id UUID NOT NULL REFERENCES business_cases(id),
    filing_number TEXT NOT NULL,
    system_name TEXT NOT NULL,
    organization_name TEXT NOT NULL,
    assessment_org TEXT NOT NULL,
    report_type TEXT NOT NULL CHECK (report_type IN ('routine', 'change')),
    reporter TEXT NOT NULL,
    contact TEXT NOT NULL DEFAULT '',
    planned_start DATE NOT NULL,
    planned_end DATE NOT NULL CHECK (planned_end >= planned_start),
    plan TEXT NOT NULL,
    created_by TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN (
        'draft', 'submitted', 'pending_initial', 'pending_final', 'pending_supervision', 'approved', 'returned', 'withdrawn'
    )),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    submitted_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    quota_year INTEGER,
    CHECK ((report_type = 'change' AND status <> 'draft') = (quota_year IS NOT NULL)),
    CHECK ((status <> 'draft') = (submitted_at IS NOT NULL)),
    CHECK (status NOT IN ('pending_initial', 'pending_final', 'pending_supervision', 'approved', 'returned') OR accepted_at IS NOT NULL),
    CHECK (status <> 'withdrawn' OR accepted_at IS NULL)
);
CREATE INDEX implementation_reports_created ON implementation_reports (created_at DESC, id DESC);
CREATE INDEX implementation_reports_filing ON implementation_reports (filing_id);
CREATE INDEX implementation_reports_quota ON implementation_reports (filing_id, quota_year)
    WHERE quota_year IS NOT NULL AND status <> 'withdrawn';

CREATE TABLE implementation_events (
    report_id UUID NOT NULL REFERENCES implementation_reports(id),
    version BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('create', 'edit', 'submit', 'accept', 'approve', 'return_for_correction', 'withdraw')),
    from_status TEXT NOT NULL,
    to_status TEXT NOT NULL,
    operator TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (report_id, version)
);
