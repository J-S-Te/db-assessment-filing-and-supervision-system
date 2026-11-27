CREATE TABLE report_compliance_records (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL REFERENCES business_cases(id),
    report_version bigint NOT NULL CHECK (report_version > 0),
    attachment_id uuid NOT NULL,
    extraction_version bigint NOT NULL,
    field jsonb NOT NULL CHECK (jsonb_typeof(field) = 'object'),
    knowledge_id uuid NOT NULL,
    knowledge_version bigint NOT NULL,
    decision text NOT NULL CHECK (decision IN ('compliant','needs_remediation','insufficient_evidence')),
    comment text NOT NULL CHECK (char_length(comment) BETWEEN 1 AND 1000),
    operator text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (attachment_id, extraction_version) REFERENCES report_extraction_reviews(attachment_id, version),
    FOREIGN KEY (knowledge_id, knowledge_version) REFERENCES knowledge_revisions(entry_id, version)
);

CREATE INDEX report_compliance_recent ON report_compliance_records (report_id, sequence DESC);
