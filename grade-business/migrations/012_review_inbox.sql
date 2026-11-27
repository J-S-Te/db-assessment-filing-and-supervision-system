CREATE TABLE review_notices (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    audience TEXT NOT NULL CHECK (audience IN ('institution', 'operator')),
    resource TEXT NOT NULL,
    resource_id UUID NOT NULL,
    event_version BIGINT NOT NULL CHECK (event_version > 0),
    state TEXT NOT NULL CHECK (length(state) BETWEEN 1 AND 40),
    recipient TEXT NOT NULL CHECK (length(recipient) BETWEEN 1 AND 200),
    reference TEXT NOT NULL,
    subject TEXT NOT NULL,
    comment TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    acknowledged_at TIMESTAMPTZ,
    UNIQUE (audience, resource, resource_id, event_version, recipient),
    CHECK ((audience='institution' AND resource IN ('filings','filing-applications','implementations'))
        OR (audience='operator' AND resource IN ('remediations','hazards')))
);

CREATE INDEX review_notices_inbox ON review_notices (audience, recipient, sequence DESC);
CREATE INDEX review_notices_resource ON review_notices (resource, resource_id, sequence DESC);
