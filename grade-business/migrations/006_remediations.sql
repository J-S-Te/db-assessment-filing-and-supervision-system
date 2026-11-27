CREATE TABLE remediation_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id UUID NOT NULL REFERENCES business_cases(id),
    deadline DATE NOT NULL,
    risks JSONB NOT NULL CHECK (jsonb_typeof(risks) = 'array'
        AND jsonb_array_length(risks) BETWEEN 1 AND 50),
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending_review', 'approved', 'returned')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    submission INTEGER NOT NULL DEFAULT 0 CHECK (submission >= 0),
    submitted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK ((status = 'draft' AND submission = 0 AND submitted_at IS NULL)
        OR (status <> 'draft' AND submission > 0 AND submitted_at IS NOT NULL))
);
CREATE INDEX remediation_plans_created ON remediation_plans(created_at DESC, id DESC);
CREATE INDEX remediation_plans_status_created
    ON remediation_plans(status, created_at DESC, id DESC);
CREATE INDEX remediation_plans_deadline
    ON remediation_plans(deadline) WHERE status <> 'draft';

CREATE TABLE remediation_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES remediation_plans(id),
    kind TEXT NOT NULL CHECK (kind IN ('plan', 'responsibility', 'schedule')),
    file_name TEXT NOT NULL,
    file_key TEXT NOT NULL UNIQUE,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX remediation_attachments_by_plan
    ON remediation_attachments(plan_id, uploaded_at DESC, id DESC);

CREATE TABLE remediation_review_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plan_id UUID NOT NULL REFERENCES remediation_plans(id),
    action TEXT NOT NULL CHECK (action IN ('submit', 'approve', 'return_for_correction')),
    from_status TEXT NOT NULL CHECK (from_status IN ('draft', 'pending_review', 'returned')),
    to_status TEXT NOT NULL CHECK (to_status IN ('pending_review', 'approved', 'returned')),
    operator TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    deadline DATE NOT NULL,
    submission INTEGER NOT NULL CHECK (submission > 0),
    version BIGINT NOT NULL CHECK (version > 0),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (plan_id, version),
    UNIQUE (plan_id, submission, from_status)
);
CREATE INDEX remediation_first_reviews ON remediation_review_events(occurred_at)
    WHERE submission = 1 AND action IN ('approve', 'return_for_correction');
