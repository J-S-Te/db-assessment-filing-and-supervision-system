ALTER TABLE remediation_review_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text;
