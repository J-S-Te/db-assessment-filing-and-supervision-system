INSERT INTO review_notices
    (audience, resource, resource_id, event_version, state, recipient, reference, subject, comment)
SELECT 'institution', 'filings', c.id, c.version, c.status, c.assessment_org, c.number, c.system_name, ''
FROM business_cases c
WHERE c.kind = 'filing' AND c.operator_origin AND c.status = 'pending_initial'
UNION ALL
SELECT 'institution', 'filing-applications', a.id, a.version, a.status,
       a.before_values->>'assessmentOrg', a.filing_number, a.before_values->>'systemName', ''
FROM filing_applications a
WHERE a.operator_origin AND a.status = 'pending_initial'
ON CONFLICT DO NOTHING;
