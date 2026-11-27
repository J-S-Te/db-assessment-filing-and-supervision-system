CREATE TABLE local_alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hazard_id uuid NOT NULL REFERENCES hazard_items(id),
    kind text NOT NULL CHECK (kind IN ('high_risk', 'overdue')),
    severity text NOT NULL CHECK (severity IN ('high', 'medium', 'low')),
    system_name text NOT NULL,
    organization_name text NOT NULL,
    deadline date NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'closed')),
    version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    triggered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    closed_at timestamptz,
    UNIQUE (hazard_id, kind),
    CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);

CREATE INDEX local_alerts_recent ON local_alerts (triggered_at DESC, id DESC);
CREATE INDEX local_alerts_status_recent ON local_alerts (status, triggered_at DESC, id DESC);
CREATE INDEX local_alerts_severity_recent ON local_alerts (severity, triggered_at DESC, id DESC);

CREATE TABLE local_alert_events (
    alert_id uuid NOT NULL REFERENCES local_alerts(id),
    version bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('create', 'process', 'close')),
    from_status text CHECK (from_status IN ('pending', 'processing')),
    to_status text NOT NULL CHECK (to_status IN ('pending', 'processing', 'closed')),
    operator text NOT NULL,
    comment text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (alert_id, version),
    CHECK ((action = 'create') = (from_status IS NULL)),
    CHECK ((action = 'create' AND to_status = 'pending' AND version = 1 AND comment = '')
        OR (action = 'process' AND from_status = 'pending' AND to_status = 'processing' AND version = 2
            AND length(comment) BETWEEN 1 AND 1000)
        OR (action = 'close' AND from_status IN ('pending', 'processing') AND to_status = 'closed' AND version > 1
            AND length(comment) BETWEEN 1 AND 1000))
);
