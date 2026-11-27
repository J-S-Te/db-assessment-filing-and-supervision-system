CREATE TABLE knowledge_tag_catalog (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 32 AND btrim(name) = name),
    name_key text COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED,
    color text NOT NULL DEFAULT '#64748b' CHECK (color ~ '^#[0-9a-f]{6}$'),
    active boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CONSTRAINT knowledge_tag_catalog_name_unique UNIQUE (name_key)
);

WITH labels AS (
    SELECT unnest(tags) AS name FROM knowledge_entries
    UNION ALL
    SELECT unnest(tags) AS name FROM knowledge_revisions
)
INSERT INTO knowledge_tag_catalog (name)
SELECT DISTINCT ON (lower(name) COLLATE "C") name
FROM labels WHERE char_length(name) BETWEEN 1 AND 32 AND btrim(name) = name
ORDER BY lower(name) COLLATE "C", name COLLATE "C";

-- register_knowledge_tag_assignments 同事务登记新关联，停用的已有引用可以保留。
-- 参数 无，使用触发器提供的变更前后标签列表。
-- 返回值 可保存的知识条目，新关联停用标签时抛出约束错误。
CREATE FUNCTION register_knowledge_tag_assignments() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    old_tags text[] := '{}';
    new_tag text;
    enabled boolean;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        old_tags := OLD.tags;
    END IF;
    FOR new_tag IN
        SELECT min(candidate.tag COLLATE "C") FROM unnest(NEW.tags) AS candidate(tag)
        WHERE NOT EXISTS (
            SELECT 1 FROM unnest(old_tags) AS previous(tag)
            WHERE lower(candidate.tag) COLLATE "C" = lower(previous.tag) COLLATE "C"
        )
        GROUP BY lower(candidate.tag) COLLATE "C"
        ORDER BY lower(candidate.tag) COLLATE "C"
    LOOP
        -- 同名并发登记与目录启停在同一行上串行，目录版本只由管理操作更新。
        INSERT INTO knowledge_tag_catalog AS catalog (name) VALUES (new_tag)
        ON CONFLICT (name_key) DO UPDATE SET name = catalog.name
        RETURNING catalog.active INTO enabled;
        IF NOT enabled THEN
            RAISE EXCEPTION USING ERRCODE = '23514', CONSTRAINT = 'knowledge_tag_active_assignment',
                MESSAGE = 'Disabled tag cannot be newly assigned';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;

CREATE TRIGGER knowledge_register_tag_assignments
BEFORE INSERT OR UPDATE OF tags ON knowledge_entries
FOR EACH ROW EXECUTE FUNCTION register_knowledge_tag_assignments();
