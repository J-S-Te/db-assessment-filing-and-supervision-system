ALTER TABLE implementation_reports
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD CONSTRAINT implementation_reports_archive_status_check
        CHECK (archived_at IS NULL OR status = 'approved');

ALTER TABLE implementation_events
    DROP CONSTRAINT implementation_events_action_check,
    ADD CONSTRAINT implementation_events_action_check CHECK (action IN (
        'create', 'edit', 'submit', 'accept', 'approve', 'return_for_correction', 'withdraw', 'archive'
    ));

CREATE INDEX implementation_reports_archived_created ON implementation_reports (created_at DESC, id DESC)
    WHERE archived_at IS NOT NULL;
