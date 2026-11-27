ALTER TABLE filing_prechecks ADD COLUMN check_results jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(check_results) = 'array');
ALTER TABLE filing_prechecks
    ADD COLUMN check_profile text NOT NULL DEFAULT '',
    ADD COLUMN check_source text NOT NULL DEFAULT ''
        CHECK (check_source IN ('', 'local_rules', 'deepseek')),
    ADD COLUMN fallback_reason text NOT NULL DEFAULT '';

CREATE TABLE filing_material_reviews (
    filing_id uuid NOT NULL REFERENCES business_cases(id),
    material_revision bigint NOT NULL CHECK (material_revision >= 0),
    rule_id text NOT NULL CHECK (rule_id IN (
        'object_number','system_and_unit','classification_date','seals','responsible_person',
        'protection_level','assets','business_description','carried_data',
        'responsible_entity','filing_certificate'
    )),
    version bigint NOT NULL CHECK (version > 0),
    status text NOT NULL CHECK (status IN ('pending','pass','fail')),
    remark text NOT NULL CHECK (char_length(remark) <= 2000),
    reviewer_id uuid NOT NULL REFERENCES access_accounts(id),
    reviewer_name text NOT NULL,
    reviewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (filing_id, material_revision, rule_id, version)
);
