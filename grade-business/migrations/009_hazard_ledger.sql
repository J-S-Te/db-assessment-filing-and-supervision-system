CREATE TABLE hazard_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES remediation_plans(id),
    risk_index INTEGER NOT NULL CHECK (risk_index > 0),
    severity TEXT NOT NULL CHECK (severity IN ('high', 'medium', 'low')),
    description TEXT NOT NULL CHECK (length(description) BETWEEN 1 AND 300),
    measure TEXT NOT NULL CHECK (length(measure) BETWEEN 1 AND 600),
    responsibility TEXT NOT NULL CHECK (length(responsibility) BETWEEN 1 AND 160),
    deadline DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','pending_review','returned','closed')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    closed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (plan_id, risk_index),
    CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);
CREATE INDEX hazard_items_created ON hazard_items(created_at DESC, id DESC);
CREATE INDEX hazard_items_by_status ON hazard_items(status, created_at DESC, id DESC);
CREATE INDEX hazard_items_open_deadline ON hazard_items(deadline) WHERE status <> 'closed';

CREATE TABLE hazard_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hazard_id UUID NOT NULL REFERENCES hazard_items(id),
    kind TEXT NOT NULL CHECK (kind = 'evidence'),
    file_name TEXT NOT NULL,
    file_key TEXT NOT NULL UNIQUE,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    item_version BIGINT NOT NULL CHECK (item_version > 1),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (hazard_id, item_version)
);
CREATE INDEX hazard_attachments_by_item
    ON hazard_attachments(hazard_id, uploaded_at DESC, id DESC);

CREATE TABLE hazard_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hazard_id UUID NOT NULL REFERENCES hazard_items(id),
    action TEXT NOT NULL,
    from_status TEXT NOT NULL,
    to_status TEXT NOT NULL,
    operator TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL CHECK (version > 1),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (hazard_id, version),
    CHECK ((action = 'submit' AND from_status IN ('open','returned') AND to_status = 'pending_review')
        OR (action = 'return_for_correction' AND from_status = 'pending_review' AND to_status = 'returned')
        OR (action = 'close' AND from_status = 'pending_review' AND to_status = 'closed')),
    CHECK (length(comment) <= 1000 AND (action = 'close' OR length(comment) > 0))
);
CREATE INDEX hazard_events_return_version ON hazard_events(hazard_id, version DESC)
    WHERE action = 'return_for_correction';
