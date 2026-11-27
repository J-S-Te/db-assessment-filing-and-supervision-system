-- 每一个有效期周期的临期与到期通知分别去重，不占用普通流程通知的幂等键。
DROP INDEX review_notices_event_once;
CREATE UNIQUE INDEX review_notices_event_once
    ON review_notices (audience, resource, resource_id, event_version, recipient)
    WHERE reminder_day IS NULL AND workflow_day IS NULL
      AND NOT (resource = 'material-expiries' AND state IN ('material_due', 'material_expired'));

CREATE UNIQUE INDEX review_notices_material_date_once
    ON review_notices (audience, resource, resource_id, event_version, recipient, state)
    WHERE resource = 'material-expiries' AND state IN ('material_due', 'material_expired');
