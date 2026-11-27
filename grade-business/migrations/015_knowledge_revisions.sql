ALTER TABLE knowledge_entries
    ADD COLUMN inactive_at timestamptz;

-- 对完整标题和来源做最终相等校验，索引以固定长度支持现有多字节值。
CREATE INDEX knowledge_import_key_idx ON knowledge_entries (category, md5(title), md5(source));

CREATE TABLE knowledge_revisions (
    entry_id uuid NOT NULL REFERENCES knowledge_entries (id),
    version bigint NOT NULL CHECK (version > 0),
    title text NOT NULL,
    category text NOT NULL,
    source text NOT NULL,
    body text NOT NULL,
    tags text[] NOT NULL,
    updated_at timestamptz NOT NULL,
    inactive_at timestamptz,
    action text NOT NULL CHECK (action IN ('baseline', 'create', 'import', 'edit', 'tags', 'disable', 'restore')),
    PRIMARY KEY (entry_id, version)
);

INSERT INTO knowledge_revisions (entry_id, version, title, category, source, body, tags, updated_at, inactive_at, action)
SELECT id, version, title, category, source, body, tags, updated_at, inactive_at, 'baseline'
FROM knowledge_entries;
