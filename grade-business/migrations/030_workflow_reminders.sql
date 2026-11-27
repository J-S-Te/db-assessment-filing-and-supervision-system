ALTER TABLE review_notices ADD COLUMN workflow_day date;

ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource;
ALTER TABLE review_notices ADD CONSTRAINT review_notices_audience_resource CHECK (
    (audience = 'institution' AND resource IN ('filings', 'filing-applications', 'implementations', 'hazards'))
    OR (audience = 'institution' AND resource = 'reports' AND workflow_day IS NOT NULL)
    OR (audience = 'operator' AND resource IN ('filings', 'filing-applications', 'implementations',
        'remediations', 'hazards', 'alerts'))
);

ALTER TABLE review_notices ADD CONSTRAINT review_notices_workflow_day CHECK (
    (workflow_day IS NULL AND state NOT IN ('retest_reminder', 'deadline_due_soon', 'deadline_due', 'deadline_overdue'))
    OR (workflow_day IS NOT NULL AND reminder_day IS NULL AND (
        (state = 'retest_reminder' AND audience = 'institution' AND resource = 'hazards')
        OR state IN ('deadline_due_soon', 'deadline_due', 'deadline_overdue')
    ))
);

-- 普通流转消息、旧整改催办、复测催办和期限提醒使用各自的业务幂等键。
DROP INDEX review_notices_event_once;
CREATE UNIQUE INDEX review_notices_event_once
    ON review_notices (audience, resource, resource_id, event_version, recipient)
    WHERE reminder_day IS NULL AND workflow_day IS NULL;

CREATE UNIQUE INDEX review_notices_retest_once
    ON review_notices (resource_id, workflow_day)
    WHERE state = 'retest_reminder';

CREATE UNIQUE INDEX review_notices_deadline_once
    ON review_notices (resource, resource_id, event_version, audience, recipient, state, workflow_day)
    WHERE state IN ('deadline_due_soon', 'deadline_due', 'deadline_overdue');
