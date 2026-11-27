ALTER VIEW publication_recipient_roster RENAME TO publication_filing_recipients;

CREATE VIEW publication_recipient_roster AS
SELECT CASE p.kind WHEN 'unit' THEN 'operator' ELSE 'institution' END AS audience,
       p.scope_name AS recipient, p.name AS display_name, p.id AS directory_id
FROM directory_parties p
WHERE p.status='active' AND (p.kind='unit'
    OR p.valid_until >= (statement_timestamp() AT TIME ZONE 'Asia/Shanghai')::date)
UNION ALL
SELECT f.audience, f.recipient, f.recipient, NULL::uuid
FROM publication_filing_recipients f
WHERE NOT EXISTS (
    SELECT 1 FROM directory_parties p
    WHERE p.kind=CASE f.audience WHEN 'operator' THEN 'unit' ELSE 'institution' END
      AND (lower(p.scope_name)=lower(f.recipient) OR lower(p.name)=lower(f.recipient))
);

ALTER TABLE publication_deliveries
    ADD COLUMN recipient_directory_id uuid REFERENCES directory_parties(id),
    ADD COLUMN recipient_display_name text
        CHECK (recipient_display_name IS NULL OR length(recipient_display_name) BETWEEN 1 AND 200);
