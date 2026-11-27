CREATE TABLE knowledge_clause_revisions (
    knowledge_id uuid NOT NULL,
    knowledge_version bigint NOT NULL,
    number text NOT NULL CHECK (char_length(number) BETWEEN 1 AND 48),
    version bigint NOT NULL CHECK (version > 0),
    text text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 4000),
    active boolean NOT NULL,
    request_id uuid NOT NULL UNIQUE,
    PRIMARY KEY (knowledge_id, knowledge_version, number, version),
    FOREIGN KEY (knowledge_id, knowledge_version) REFERENCES knowledge_revisions(entry_id, version)
);

ALTER TABLE report_compliance_records
    ADD COLUMN request_id uuid UNIQUE,
    ADD COLUMN clause_number text,
    ADD COLUMN clause_version bigint,
    ADD CONSTRAINT report_compliance_clause_pair
        CHECK ((clause_number IS NULL) = (clause_version IS NULL)),
    ADD FOREIGN KEY (knowledge_id, knowledge_version, clause_number, clause_version)
        REFERENCES knowledge_clause_revisions(knowledge_id, knowledge_version, number, version);
