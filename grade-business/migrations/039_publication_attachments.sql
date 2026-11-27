CREATE TABLE publication_attachments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    publication_id uuid NOT NULL REFERENCES local_publications(id),
    file_name text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 150),
    file_key text NOT NULL UNIQUE CHECK (file_key ~ '^[0-9a-f]{48}$'),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520),
    uploaded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (publication_id,id),
    UNIQUE (publication_id,file_name,digest)
);

CREATE TABLE publication_version_attachments (
    publication_id uuid NOT NULL,
    version bigint NOT NULL,
    attachment_id uuid NOT NULL,
    position smallint NOT NULL CHECK (position BETWEEN 1 AND 8),
    PRIMARY KEY(publication_id,version,attachment_id),
    UNIQUE (publication_id,version,position),
    FOREIGN KEY (publication_id,version) REFERENCES publication_events(publication_id,version),
    FOREIGN KEY (publication_id,attachment_id) REFERENCES publication_attachments(publication_id,id)
);
