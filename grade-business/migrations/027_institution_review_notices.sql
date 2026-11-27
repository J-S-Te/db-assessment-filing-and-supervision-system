ALTER TABLE review_notices DROP CONSTRAINT review_notices_audience_resource;
ALTER TABLE review_notices ADD CONSTRAINT review_notices_audience_resource CHECK (
    (audience = 'institution' AND resource IN ('filings', 'filing-applications', 'implementations', 'hazards'))
    OR
    (audience = 'operator' AND resource IN ('filings', 'filing-applications', 'implementations',
        'remediations', 'hazards', 'alerts'))
);
