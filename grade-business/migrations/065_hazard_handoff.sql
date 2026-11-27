ALTER TABLE hazard_items ADD COLUMN handoff_version bigint
    CHECK (handoff_version IS NULL OR (handoff_version > 1 AND handoff_version <= version));
ALTER TABLE hazard_attachments ADD COLUMN merge_history_scope text;
ALTER TABLE hazard_events ADD COLUMN merge_history_number text, ADD COLUMN merge_history_system_name text;
