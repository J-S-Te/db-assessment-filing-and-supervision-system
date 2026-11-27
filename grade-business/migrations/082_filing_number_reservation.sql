ALTER TABLE business_cases ADD COLUMN filing_number_provisional boolean NOT NULL DEFAULT false;

-- 旧记录未区分机构代报与监管录入。保留运营者现有流程和有导入留痕的历史号。
WITH imported_filings AS (
    SELECT DISTINCT entry->>'filingId' AS id
    FROM filing_history_import_batches
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(result->'rows', '[]'::jsonb)) AS entry
    WHERE entry->>'status' = 'imported'
)
UPDATE business_cases AS c SET filing_number_provisional = true
WHERE c.kind = 'filing' AND c.discarded_at IS NULL AND NOT c.operator_origin
    AND c.status IN ('draft', 'pending_initial', 'pending_final', 'returned')
    AND NOT EXISTS (SELECT 1 FROM imported_filings AS i WHERE i.id = c.id::text);

ALTER TABLE business_cases ADD CONSTRAINT business_cases_provisional_filing_number
    CHECK (NOT filing_number_provisional OR (kind = 'filing' AND status <> 'approved'));

DROP INDEX business_cases_kind_number;
CREATE UNIQUE INDEX business_cases_kind_number ON business_cases(kind,lower(number))
    WHERE discarded_at IS NULL AND status<>'withdrawn' AND number<>'' AND NOT filing_number_provisional;
