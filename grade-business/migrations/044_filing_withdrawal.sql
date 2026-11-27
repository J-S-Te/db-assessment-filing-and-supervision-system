ALTER TABLE business_cases
    DROP CONSTRAINT business_cases_status_check,
    ADD CONSTRAINT business_cases_status_check
        CHECK (status IN ('draft','pending_initial','pending_final','approved','returned')
            OR (status='withdrawn' AND kind='filing' AND operator_origin));

-- 已确认撤回的首报留档，备案号允许新报送继续使用。
DROP INDEX business_cases_kind_number;
CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind,lower(number))
    WHERE discarded_at IS NULL AND status<>'withdrawn';

ALTER TABLE case_review_events
    DROP CONSTRAINT case_review_events_action_check,
    DROP CONSTRAINT case_review_events_to_status_check,
    ADD CONSTRAINT case_review_events_action_check
        CHECK (action IN ('submit','approve','return_for_correction','withdraw')),
    ADD CONSTRAINT case_review_events_to_status_check
        CHECK (to_status IN ('draft','pending_initial','pending_final','approved','returned','withdrawn')),
    ADD CONSTRAINT case_review_events_withdrawal_check
        CHECK ((action='withdraw')=(to_status='withdrawn')
            AND (action<>'withdraw' OR from_status IN ('pending_initial','pending_final')));

ALTER TABLE filing_application_review_events
    DROP CONSTRAINT filing_application_review_events_withdrawal_check,
    ADD CONSTRAINT filing_application_review_events_withdrawal_check
        CHECK ((action='withdraw')=(to_status='withdrawn')
            AND (action<>'withdraw' OR from_status IN ('draft','returned','pending_initial','pending_final')));

CREATE TABLE filing_withdrawal_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource text NOT NULL CHECK (resource IN ('filings','filing-applications')),
    filing_id uuid REFERENCES business_cases(id),
    application_id uuid REFERENCES filing_applications(id),
    resource_id uuid GENERATED ALWAYS AS (COALESCE(filing_id,application_id)) STORED,
    requested_from_version bigint NOT NULL CHECK (requested_from_version>0),
    case_version bigint NOT NULL,
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    comment text,
    decided_by text,
    decided_at timestamptz,
    UNIQUE(resource,resource_id,requested_from_version),
    CHECK ((resource='filings' AND filing_id IS NOT NULL AND application_id IS NULL)
        OR (resource='filing-applications' AND filing_id IS NULL AND application_id IS NOT NULL)),
    CHECK ((status='pending' AND case_version=requested_from_version+1
            AND decided_at IS NULL AND comment IS NULL AND decided_by IS NULL)
        OR (status<>'pending' AND case_version=requested_from_version+2
            AND decided_at IS NOT NULL AND comment IS NOT NULL AND decided_by IS NOT NULL))
);
CREATE UNIQUE INDEX filing_withdrawal_pending ON filing_withdrawal_requests(resource,resource_id)
    WHERE status='pending';

-- 暂停原审核队列，撤回申请只呈现一个监管确认待办。驳回后从原审核阶段恢复。
ALTER VIEW workflow_tasks RENAME TO workflow_active_tasks;
CREATE VIEW workflow_tasks AS
SELECT t.id,t.resource,
       CASE WHEN w.id IS NULL THEN t.label ELSE '备案撤回确认' END AS label,
       t.system_name,t.number,t.status,t.organization,t.institution,t.applicant_audience,
       CASE WHEN w.id IS NULL THEN t.audience ELSE 'regulator' END AS audience,
       COALESCE(w.requested_at,t.stage_at) AS stage_at,
       COALESCE(w.case_version,t.stage_version) AS stage_version,
       CASE WHEN w.id IS NULL THEN t.window_kind ELSE '' END AS window_kind,
       CASE WHEN w.id IS NULL THEN t.explicit_deadline END AS explicit_deadline,
       CASE WHEN w.id IS NULL THEN t.recipient ELSE '' END AS recipient
FROM workflow_active_tasks t
LEFT JOIN filing_withdrawal_requests w ON w.resource=t.resource AND w.resource_id=t.id AND w.status='pending';
