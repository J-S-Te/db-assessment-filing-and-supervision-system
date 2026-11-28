ALTER TABLE filing_withdrawal_requests
    ADD COLUMN decided_by_id uuid REFERENCES access_accounts(id),
    ADD COLUMN decided_by_role text;
