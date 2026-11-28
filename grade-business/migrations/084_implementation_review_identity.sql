ALTER TABLE implementation_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text,
    DROP CONSTRAINT implementation_events_action_check,
    ADD CONSTRAINT implementation_events_action_check CHECK (action IN (
        'create', 'edit', 'submit', 'accept', 'approve', 'return_for_correction', 'withdraw', 'archive',
        'restart_initial'
    ));
