ALTER TABLE local_alert_events
    DROP CONSTRAINT local_alert_events_action_check,
    DROP CONSTRAINT local_alert_events_check1,
    ADD CONSTRAINT local_alert_events_action_check CHECK (action IN (
        'create','process','close','convert','request_extension','report_false_alarm','approve','reject'
    )),
    ADD CONSTRAINT local_alert_events_transition_check CHECK (
        (action = 'create' AND to_status = 'pending' AND version = 1 AND comment = '')
        OR (action = 'convert' AND from_status IN ('pending','processing') AND to_status = 'processing'
            AND version > 1 AND length(comment) <= 1000)
        OR (version > 1 AND length(comment) BETWEEN 1 AND 1000 AND (
            (action = 'process' AND from_status = 'pending' AND to_status = 'processing')
            OR (action IN ('close','approve') AND to_status = 'closed')
            OR (action IN ('request_extension','report_false_alarm','approve','reject') AND from_status = to_status)
        ))
    );

ALTER TABLE hazard_events
    DROP CONSTRAINT hazard_events_check,
    DROP CONSTRAINT hazard_events_check1,
    ADD CONSTRAINT hazard_events_transition_check CHECK (
        (action = 'submit' AND from_status IN ('open','returned') AND to_status = 'pending_review')
        OR (action = 'return_for_correction' AND from_status = 'pending_review' AND to_status = 'returned')
        OR (action = 'close' AND from_status = 'pending_review' AND to_status = 'closed')
        OR (action = 'extend_deadline' AND from_status IN ('open','returned','pending_review') AND from_status = to_status)
    ),
    ADD CONSTRAINT hazard_events_comment_check CHECK (
        (action = 'extend_deadline' AND length(comment) BETWEEN 1 AND 1040)
        OR (action <> 'extend_deadline' AND length(comment) <= 1000 AND (action = 'close' OR length(comment) > 0))
    );

CREATE TABLE local_alert_dispositions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id uuid NOT NULL REFERENCES local_alerts(id),
    hazard_id uuid NOT NULL REFERENCES hazard_items(id),
    action text NOT NULL CHECK (action IN ('convert','request_extension','report_false_alarm')),
    status text NOT NULL CHECK (status IN ('completed','pending','approved','rejected')),
    comment text NOT NULL DEFAULT '' CHECK (length(comment) <= 1000 AND (action = 'convert' OR length(comment) > 0)),
    requested_deadline date,
    requested_by text NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    requested_version bigint NOT NULL,
    decision_comment text,
    decided_by text,
    decided_at timestamptz,
    decision_version bigint,
    UNIQUE (alert_id, requested_version),
    FOREIGN KEY (alert_id, requested_version) REFERENCES local_alert_events(alert_id,version),
    FOREIGN KEY (alert_id, decision_version) REFERENCES local_alert_events(alert_id,version),
    CHECK ((action = 'convert') = (status = 'completed')),
    CHECK ((action = 'request_extension') = (requested_deadline IS NOT NULL)),
    CHECK (
        (status IN ('pending','completed') AND decision_comment IS NULL AND decided_by IS NULL
            AND decided_at IS NULL AND decision_version IS NULL)
        OR (status IN ('approved','rejected') AND decision_comment IS NOT NULL AND decided_by IS NOT NULL
            AND length(decision_comment) BETWEEN 1 AND 1000 AND decided_at IS NOT NULL
            AND decision_version IS NOT NULL AND decision_version > requested_version)
    )
);

CREATE UNIQUE INDEX local_alert_one_pending_disposition ON local_alert_dispositions(hazard_id) WHERE status = 'pending';
CREATE UNIQUE INDEX local_alert_one_hazard_conversion ON local_alert_dispositions(hazard_id) WHERE action = 'convert';
CREATE INDEX local_alert_dispositions_decision_history ON local_alert_dispositions(alert_id,decision_version)
    WHERE decision_version IS NOT NULL;

INSERT INTO review_notices (audience,resource,resource_id,event_version,state,recipient,reference,subject,comment)
SELECT 'operator','alerts',a.id,1,'pending',f.organization_name,f.number,f.system_name,''
FROM local_alerts a
JOIN hazard_items h ON h.id=a.hazard_id
JOIN remediation_plans p ON p.id=h.plan_id
JOIN business_cases r ON r.id=p.report_id AND r.kind='report'
JOIN business_cases f ON f.id=r.filing_id AND f.kind='filing'
WHERE a.status<>'closed' AND f.organization_name<>''
ON CONFLICT DO NOTHING;
