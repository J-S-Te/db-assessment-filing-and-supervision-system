CREATE TABLE report_feedback (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES case_review_events(id),
    id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    institution TEXT NOT NULL,
    report_number TEXT NOT NULL,
    system_name TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    acknowledged_at TIMESTAMPTZ,
    UNIQUE (event_id, institution)
);

CREATE INDEX report_feedback_institution_recent
    ON report_feedback (institution, sequence DESC);
