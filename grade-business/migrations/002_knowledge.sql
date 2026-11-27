CREATE TABLE knowledge_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    category text NOT NULL CHECK (category IN ('regulation', 'standard', 'vulnerability', 'case', 'template')),
    source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 500),
    body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 32768),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX knowledge_recent_idx ON knowledge_entries (updated_at DESC, id DESC);
CREATE INDEX knowledge_category_recent_idx ON knowledge_entries (category, updated_at DESC, id DESC);
