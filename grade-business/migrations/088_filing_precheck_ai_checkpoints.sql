ALTER TABLE filing_prechecks
    ADD COLUMN force_ai boolean NOT NULL DEFAULT false,
    ADD COLUMN unit_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE filing_precheck_history
    ADD COLUMN force_ai boolean NOT NULL DEFAULT false,
    ADD COLUMN unit_fingerprint text NOT NULL DEFAULT '';

CREATE TABLE filing_precheck_ai_checkpoints (
    filing_id uuid NOT NULL REFERENCES business_cases(id) ON DELETE CASCADE,
    policy_version bigint NOT NULL,
    input_key text NOT NULL CHECK (input_key ~ '^[a-f0-9]{64}$'),
    job_id uuid NOT NULL,
    result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object'),
    PRIMARY KEY (filing_id, policy_version, input_key)
);

CREATE TABLE filing_precheck_ai_usage (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    filing_id uuid NOT NULL REFERENCES business_cases(id) ON DELETE CASCADE,
    job_id uuid NOT NULL,
    claim_token uuid NOT NULL,
    stage text NOT NULL CHECK (stage IN ('deepseek', 'deepseek_summary')),
    model text NOT NULL,
    provider_request_id text NOT NULL,
    outcome text NOT NULL,
    usage_reported boolean NOT NULL,
    cache_reported boolean NOT NULL,
    prompt_tokens bigint NOT NULL CHECK (prompt_tokens >= 0),
    prompt_cache_hit_tokens bigint NOT NULL CHECK (prompt_cache_hit_tokens >= 0),
    prompt_cache_miss_tokens bigint NOT NULL CHECK (prompt_cache_miss_tokens >= 0),
    completion_tokens bigint NOT NULL CHECK (completion_tokens >= 0),
    reasoning_tokens bigint NOT NULL CHECK (reasoning_tokens >= 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX filing_precheck_ai_usage_job ON filing_precheck_ai_usage(job_id, id);
