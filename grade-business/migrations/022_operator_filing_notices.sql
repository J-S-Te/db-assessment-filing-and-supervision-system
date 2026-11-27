ALTER TABLE review_notices DROP CONSTRAINT review_notices_check;
ALTER TABLE review_notices ADD CONSTRAINT review_notices_audience_resource CHECK (
    (audience = 'institution' AND resource IN ('filings', 'filing-applications', 'implementations'))
    OR
    (audience = 'operator' AND resource IN ('filings', 'filing-applications', 'implementations', 'remediations', 'hazards'))
);

CREATE INDEX business_cases_filing_owner_created
    ON business_cases (organization_name, created_at DESC, id DESC)
    WHERE kind = 'filing';

ALTER TABLE business_cases
    ADD COLUMN operator_origin boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT business_cases_operator_filing_origin CHECK (NOT operator_origin OR kind = 'filing');
