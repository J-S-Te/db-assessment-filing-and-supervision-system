CREATE UNIQUE INDEX case_attachments_case_id_identity ON case_attachments(case_id,id);

CREATE TABLE assessment_result_revisions (
    report_id uuid NOT NULL REFERENCES business_cases(id),
    version bigint NOT NULL CHECK (version > 0),
    report_version bigint NOT NULL CHECK (report_version > 0),
    evidence_id uuid NOT NULL,
    score_hundredths integer NOT NULL CHECK (score_hundredths BETWEEN 0 AND 10000),
    conclusion text NOT NULL CHECK (conclusion IN ('conforming','basically_conforming','nonconforming')),
    high_count integer NOT NULL CHECK (high_count BETWEEN 0 AND 100000),
    medium_count integer NOT NULL CHECK (medium_count BETWEEN 0 AND 100000),
    low_count integer NOT NULL CHECK (low_count BETWEEN 0 AND 100000),
    actor text NOT NULL CHECK (char_length(actor) BETWEEN 1 AND 200),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (report_id,version),
    FOREIGN KEY (report_id,evidence_id) REFERENCES case_attachments(case_id,id)
);

CREATE INDEX assessment_archive_order ON business_cases
    (filing_id, (COALESCE(submitted_at,created_at)) DESC, id DESC)
    WHERE kind='report' AND status='approved' AND discarded_at IS NULL;
