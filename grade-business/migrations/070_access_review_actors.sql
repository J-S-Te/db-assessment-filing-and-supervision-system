ALTER TABLE case_review_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text;

ALTER TABLE filing_application_review_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text;
