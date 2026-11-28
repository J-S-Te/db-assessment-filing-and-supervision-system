-- 业务历史保留原经办标签，新增事件使用已验证的账号和角色。
ALTER TABLE hazard_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text;

ALTER TABLE local_alert_events
    ADD COLUMN operator_id uuid REFERENCES access_accounts(id),
    ADD COLUMN operator_role text;

ALTER TABLE supervision_events
    ADD COLUMN actor_id uuid REFERENCES access_accounts(id),
    ADD COLUMN actor_role text;

ALTER TABLE supervision_site_events
    ADD COLUMN actor_id uuid REFERENCES access_accounts(id),
    ADD COLUMN actor_role text;

ALTER TABLE supervision_dispatch_batches
    ADD COLUMN actor_id uuid REFERENCES access_accounts(id),
    ADD COLUMN actor_role text;
