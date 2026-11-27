ALTER TABLE hazard_items
    ALTER COLUMN plan_id DROP NOT NULL,
    ALTER COLUMN risk_index DROP NOT NULL,
    ADD COLUMN registration_filing_id uuid REFERENCES business_cases(id),
    ADD COLUMN ledger text NOT NULL DEFAULT 'vulnerability'
        CHECK (ledger IN ('vulnerability','major_risk')),
    ADD COLUMN title text,
    ADD COLUMN source text,
    ADD COLUMN discovered_on date,
    ADD COLUMN dispatched_at timestamptz,
    ADD COLUMN retest_institution text,
    DROP CONSTRAINT hazard_items_status_check,
    ADD CONSTRAINT hazard_items_status_check
        CHECK (status IN ('pending_dispatch','open','pending_retest','pending_review','returned','closed')),
    ADD CONSTRAINT hazard_items_source_check CHECK (
        (registration_filing_id IS NULL AND plan_id IS NOT NULL AND risk_index IS NOT NULL
            AND ledger='vulnerability' AND title IS NULL AND source IS NULL AND discovered_on IS NULL
            AND retest_institution IS NULL AND dispatched_at IS NULL AND status<>'pending_dispatch')
        OR
        (registration_filing_id IS NOT NULL AND plan_id IS NULL AND risk_index IS NULL
            AND title IS NOT NULL AND length(title) BETWEEN 1 AND 160
            AND source IS NOT NULL AND source IN ('manual','assessment','scan')
            AND discovered_on IS NOT NULL AND deadline>=discovered_on
            AND ((status='pending_dispatch' AND retest_institution IS NULL AND dispatched_at IS NULL)
                 OR (status<>'pending_dispatch' AND dispatched_at IS NOT NULL
                     AND retest_institution IS NOT NULL AND length(btrim(retest_institution)) BETWEEN 1 AND 200)))
    );

-- 同一发现日的登记重试幂等。在办期间同一系统和规范化标题只会有一条。
CREATE UNIQUE INDEX hazard_registration_same_day
    ON hazard_items (registration_filing_id, lower(btrim(regexp_replace(title,'[[:space:]]+',' ','g'))), discovered_on)
    WHERE registration_filing_id IS NOT NULL;
CREATE UNIQUE INDEX hazard_registration_active
    ON hazard_items (registration_filing_id, lower(btrim(regexp_replace(title,'[[:space:]]+',' ','g'))))
    WHERE registration_filing_id IS NOT NULL AND status<>'closed';
CREATE INDEX hazard_items_ledger_cursor ON hazard_items (ledger,created_at DESC,id DESC);

CREATE VIEW hazard_context AS
SELECT h.id, filing.id AS filing_id, report.id AS report_id,
       COALESCE(h.retest_institution,report.assessment_org,'') AS assessment_org,
       CASE WHEN h.registration_filing_id IS NOT NULL THEN h.retest_institution IS NOT NULL
            ELSE COALESCE(report.status='approved',false) END AS can_retest,
       filing.system_name, filing.organization_name, filing.number AS filing_number,
       COALESCE(report.number,'') AS report_number
FROM hazard_items h
LEFT JOIN remediation_plans p ON p.id=h.plan_id
LEFT JOIN business_cases report ON report.id=p.report_id AND report.kind='report'
JOIN business_cases filing ON filing.id=COALESCE(h.registration_filing_id,report.filing_id) AND filing.kind='filing';

ALTER TABLE hazard_events DROP CONSTRAINT hazard_events_version_check,
    DROP CONSTRAINT hazard_events_transition_check,
    DROP CONSTRAINT hazard_events_comment_check,
    ADD CONSTRAINT hazard_events_version_check
        CHECK ((action='register' AND version=1) OR (action<>'register' AND version>1)),
    ADD CONSTRAINT hazard_events_transition_check CHECK (
        (action='register' AND from_status='' AND to_status='pending_dispatch')
        OR (action='dispatch' AND from_status='pending_dispatch' AND to_status='open')
        OR (action='supervise' AND from_status IN ('open','returned','pending_retest') AND from_status=to_status)
        OR (action='submit' AND from_status IN ('open','returned') AND to_status IN ('pending_retest','pending_review'))
        OR (action='retest_pass' AND from_status='pending_retest' AND to_status='pending_review')
        OR (action='retest_return' AND from_status='pending_retest' AND to_status='returned')
        OR (action='return_for_correction' AND from_status='pending_review' AND to_status='returned')
        OR (action='close' AND from_status='pending_review' AND to_status='closed')
        OR (action='extend_deadline' AND from_status IN ('open','returned','pending_retest','pending_review')
            AND from_status=to_status)
    ),
    ADD CONSTRAINT hazard_events_comment_check CHECK (
        (action='extend_deadline' AND length(comment) BETWEEN 1 AND 1040)
        OR (action<>'extend_deadline' AND length(comment)<=1000
            AND (action IN ('register','dispatch','close','retest_pass') OR length(comment)>0))
    );

ALTER TABLE review_notices DROP CONSTRAINT review_notices_workflow_day,
    ADD CONSTRAINT review_notices_workflow_day CHECK (
        (workflow_day IS NULL AND state NOT IN (
            'retest_reminder','deadline_due_soon','deadline_due','deadline_overdue','hazard_supervision'))
        OR (workflow_day IS NOT NULL AND reminder_day IS NULL AND (
            (state='retest_reminder' AND audience='institution' AND resource='hazards')
            OR (state='hazard_supervision' AND resource='hazards')
            OR state IN ('deadline_due_soon','deadline_due','deadline_overdue')
        ))
    );
CREATE UNIQUE INDEX review_notices_supervision_once
    ON review_notices (resource_id,workflow_day) WHERE state='hazard_supervision';
