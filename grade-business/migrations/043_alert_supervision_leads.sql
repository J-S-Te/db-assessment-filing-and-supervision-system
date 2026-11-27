CREATE TABLE alert_supervision_leads (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    alert_id uuid PRIMARY KEY REFERENCES local_alerts(id),
    filing_number text NOT NULL,
    system_name text NOT NULL,
    organization_name text NOT NULL,
    deadline date NOT NULL,
    admitted_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX local_alerts_closed_export ON local_alerts(closed_at DESC,id DESC)
    WHERE status='closed';
