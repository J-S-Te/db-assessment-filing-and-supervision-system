CREATE TABLE alert_config_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    request_id uuid NOT NULL UNIQUE,
    original_input jsonb NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    trigger_kind text NOT NULL CHECK (trigger_kind IN ('high_risk','overdue','filing_review_overdue')),
    audience text NOT NULL CHECK (audience IN ('regulator','institution','operator')),
    target_unit_id uuid REFERENCES directory_parties(id),
    district text NOT NULL DEFAULT '' CHECK (length(district)<=80),
    level text NOT NULL DEFAULT '' CHECK (level IN ('','1','2','3','4','5')),
    all_scope boolean NOT NULL,
    enabled boolean NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version>0),
    actor text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (all_scope = (target_unit_id IS NULL AND district='' AND level=''))
);
CREATE UNIQUE INDEX alert_config_rule_name ON alert_config_rules(lower(name));
CREATE INDEX alert_config_enabled_trigger ON alert_config_rules(trigger_kind) WHERE enabled;

CREATE TABLE alert_rule_matches (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    rule_id uuid NOT NULL REFERENCES alert_config_rules(id),
    rule_version bigint NOT NULL,
    rule_name text NOT NULL,
    resource text NOT NULL CHECK (resource IN ('alerts','filings')),
    resource_id uuid NOT NULL,
    stage_version bigint NOT NULL,
    notice_state text NOT NULL CHECK (notice_state IN ('pending','deadline_overdue')),
    reference text NOT NULL,
    system_name text NOT NULL,
    organization_name text NOT NULL,
    audience text NOT NULL CHECK (audience IN ('regulator','institution','operator')),
    recipient text NOT NULL,
    deadline date NOT NULL,
    matched_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(rule_id, resource, resource_id, stage_version, audience, recipient)
);
