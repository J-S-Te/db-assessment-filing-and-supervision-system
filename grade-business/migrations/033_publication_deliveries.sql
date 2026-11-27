ALTER TABLE publication_events
    ADD COLUMN audience text NOT NULL DEFAULT 'all'
        CHECK (audience IN ('all','operator','institution','selected')),
    ADD COLUMN targets jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT publication_policy_targets
        CHECK (jsonb_typeof(targets)='array' AND ((targets<>'[]'::jsonb) = (audience='selected')));

CREATE VIEW publication_recipient_roster AS
SELECT DISTINCT member.audience, normalized.recipient
FROM business_cases f
CROSS JOIN LATERAL (
    VALUES ('operator', f.organization_name), ('institution', f.assessment_org)
) member(audience, name)
CROSS JOIN LATERAL (
    VALUES (btrim(member.name,
        U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'))
) normalized(recipient)
WHERE f.kind='filing' AND f.status='approved' AND f.cancelled_at IS NULL
    AND char_length(normalized.recipient) BETWEEN 1 AND 200
    AND normalized.recipient !~ U&'[\0001-\001F\007F-\009F]';

CREATE TABLE publication_deliveries (
    publication_id uuid NOT NULL,
    version bigint NOT NULL,
    audience text NOT NULL CHECK (audience IN ('operator','institution')),
    recipient text NOT NULL CHECK (char_length(recipient) BETWEEN 1 AND 200),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    delivered_at timestamptz NOT NULL,
    read_at timestamptz,
    acknowledged_at timestamptz,
    PRIMARY KEY(publication_id, version, audience, recipient),
    FOREIGN KEY (publication_id,version) REFERENCES publication_events(publication_id,version),
    CHECK (read_at IS NULL OR read_at >= delivered_at),
    CHECK (acknowledged_at IS NULL OR (read_at IS NOT NULL AND acknowledged_at >= read_at))
);

-- 旧公告没有逐收件方留痕，只在迁移时为仍公开的版本实际生成送达。
INSERT INTO publication_deliveries(publication_id,version,audience,recipient,delivered_at)
SELECT publication.id,publication.published_version,roster.audience,roster.recipient,statement_timestamp()
FROM local_publications publication
JOIN publication_events event ON (event.publication_id,event.version)=(publication.id,publication.published_version)
CROSS JOIN publication_recipient_roster roster
WHERE publication.status='published' AND event.action='publish';

CREATE INDEX publication_deliveries_unread ON publication_deliveries (audience, recipient, publication_id, version)
    WHERE read_at IS NULL;
CREATE INDEX publication_deliveries_by_version ON publication_deliveries (publication_id, version, sequence DESC);
CREATE INDEX publication_events_published_versions ON publication_events (publication_id, version DESC)
    WHERE action='publish';
