CREATE TABLE filing_material_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    rules jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(rules) = 'array')
);

INSERT INTO filing_material_policy DEFAULT VALUES;

ALTER TABLE filing_prechecks
    ADD COLUMN policy_version bigint NOT NULL DEFAULT 1 CHECK (policy_version > 0),
    ADD COLUMN policy_rules jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(policy_rules) = 'array');

ALTER TABLE filing_prechecks DROP CONSTRAINT filing_prechecks_filing_id_revision_key;
ALTER TABLE filing_prechecks ADD UNIQUE (filing_id, revision, policy_version);
