ALTER TABLE supervision_tasks
    ADD COLUMN merge_history_scope text,
    ADD COLUMN merge_history_version bigint,
    ADD COLUMN merge_history_snapshot jsonb,
    ADD CONSTRAINT supervision_handoff_snapshot CHECK (
        (merge_history_scope IS NULL AND merge_history_version IS NULL AND merge_history_snapshot IS NULL)
        OR (merge_history_scope IS NOT NULL AND merge_history_version > 0
            AND merge_history_version < version AND merge_history_snapshot IS NOT NULL
            AND jsonb_typeof(merge_history_snapshot) = 'object')
    );

ALTER TABLE supervision_site_inspections
    ADD COLUMN merge_history_version bigint,
    ADD COLUMN merge_history_snapshot jsonb,
    ADD CONSTRAINT supervision_site_handoff_snapshot CHECK (
        (merge_history_version IS NULL AND merge_history_snapshot IS NULL)
        OR (merge_history_version > 0 AND merge_history_version < version
            AND merge_history_snapshot IS NOT NULL AND jsonb_typeof(merge_history_snapshot) = 'object')
    );
