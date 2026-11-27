ALTER TABLE knowledge_entries
    ADD COLUMN tags text[] NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 12),
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0);
