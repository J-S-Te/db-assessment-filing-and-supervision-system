CREATE TABLE workflow_calendar_events (
    request_id uuid PRIMARY KEY,
    day date NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    mode text NOT NULL CHECK (mode IN ('workday','restday','default')),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 300),
    operator text NOT NULL,
    before_is_workday boolean NOT NULL,
    before_override boolean NOT NULL,
    is_workday boolean NOT NULL,
    is_override boolean NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (day,version),
    CHECK (is_override=(mode<>'default'))
);
