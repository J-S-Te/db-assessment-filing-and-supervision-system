ALTER TABLE filing_applications
    DROP CONSTRAINT filing_applications_status_check,
    ADD CONSTRAINT filing_applications_status_check
        CHECK (status IN ('draft','pending_initial','pending_final','approved','returned','withdrawn'));

ALTER TABLE filing_application_review_events
    DROP CONSTRAINT filing_application_review_events_action_check,
    DROP CONSTRAINT filing_application_review_events_to_status_check,
    ADD CONSTRAINT filing_application_review_events_action_check
        CHECK (action IN ('submit','approve','return_for_correction','withdraw')),
    ADD CONSTRAINT filing_application_review_events_to_status_check
        CHECK (to_status IN ('draft','pending_initial','pending_final','approved','returned','withdrawn')),
    ADD CONSTRAINT filing_application_review_events_withdrawal_check
        CHECK ((action = 'withdraw') = (to_status = 'withdrawn')
            AND (action <> 'withdraw' OR from_status IN ('draft', 'returned')));

DROP INDEX filing_applications_one_active;
CREATE UNIQUE INDEX filing_applications_one_active ON filing_applications(filing_id)
    WHERE status IN ('draft','pending_initial','pending_final','returned');
