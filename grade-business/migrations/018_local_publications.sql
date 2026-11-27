CREATE TABLE local_publications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','withdrawn')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    published_version bigint,
    published_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((status='published') = (published_version IS NOT NULL)),
    CHECK ((status='draft') = (published_at IS NULL)),
    CHECK (published_version <= version)
);

CREATE TABLE publication_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    publication_id uuid NOT NULL REFERENCES local_publications(id),
    version bigint NOT NULL CHECK (version > 0),
    action text NOT NULL CHECK (action IN ('create','edit','publish','withdraw')),
    status text NOT NULL CHECK (status IN ('draft','published','withdrawn')),
    category text NOT NULL CHECK (category IN ('bulletin','security_advisory','announcement')),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    publisher text NOT NULL CHECK (char_length(publisher) BETWEEN 1 AND 120),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 8000),
    pinned boolean NOT NULL,
    alert_id uuid REFERENCES local_alerts(id),
    operator text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(publication_id,version),
    CHECK (alert_id IS NULL OR category='security_advisory')
);

ALTER TABLE local_publications
    ADD CONSTRAINT publication_current_snapshot FOREIGN KEY (id, version)
        REFERENCES publication_events(publication_id,version) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT publication_live_snapshot FOREIGN KEY (id, published_version)
        REFERENCES publication_events(publication_id,version) DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX publication_events_pinned ON publication_events (pinned DESC, sequence DESC);
