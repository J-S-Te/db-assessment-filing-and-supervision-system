CREATE TABLE filing_history_import_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    digest text NOT NULL UNIQUE CHECK (digest ~ '^[0-9a-f]{64}$'),
    operator text NOT NULL CHECK (char_length(operator) > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    result jsonb NOT NULL CHECK (jsonb_typeof(result) = 'object')
);
