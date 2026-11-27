CREATE TABLE report_extractions (
    attachment_id UUID PRIMARY KEY REFERENCES case_attachments(id),
    fields JSONB NOT NULL CHECK (jsonb_typeof(fields) = 'array'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    confirmed_at TIMESTAMPTZ,
    confirmed_by TEXT
);

CREATE TABLE report_extraction_reviews (
    attachment_id UUID NOT NULL REFERENCES report_extractions(attachment_id),
    version BIGINT NOT NULL,
    changes JSONB NOT NULL CHECK (jsonb_typeof(changes) = 'array'),
    operator TEXT NOT NULL,
    confirmed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (attachment_id, version)
);
