ALTER TABLE review_notices ADD COLUMN reminder_day date,
    ADD CONSTRAINT review_notices_reminder_kind CHECK (
        (state <> 'reminder' AND reminder_day IS NULL)
        OR (state = 'reminder' AND reminder_day IS NOT NULL AND audience='operator' AND resource='remediations')
    );

DO $$
DECLARE
    event_constraint name;
BEGIN
    -- 原唯一约束的名称由 PostgreSQL 自动生成并缩短。
    SELECT conname INTO STRICT event_constraint FROM pg_constraint
    WHERE conrelid='review_notices'::regclass AND contype='u'
    AND pg_get_constraintdef(oid)='UNIQUE (audience, resource, resource_id, event_version, recipient)';
    EXECUTE format('ALTER TABLE review_notices DROP CONSTRAINT %I', event_constraint);
END $$;

CREATE UNIQUE INDEX review_notices_event_once
    ON review_notices (audience, resource, resource_id, event_version, recipient)
    WHERE reminder_day IS NULL;

CREATE UNIQUE INDEX review_notices_reminder_daily
    ON review_notices (audience, resource, resource_id, recipient, reminder_day)
    WHERE reminder_day IS NOT NULL;
