ALTER TABLE filing_prechecks DROP CONSTRAINT filing_prechecks_progress_stage_check;
ALTER TABLE filing_prechecks ADD CHECK (progress_stage IN
    ('', 'materials', 'deepseek', 'deepseek_summary', 'local_rules', 'fallback'));

ALTER TABLE filing_prechecks
    ADD COLUMN claim_token uuid,
    ADD COLUMN material_reviews jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(material_reviews) = 'array'),
    ADD COLUMN material_review_total integer NOT NULL DEFAULT 0
        CHECK (material_review_total >= 0 AND jsonb_array_length(material_reviews) <= material_review_total);

-- 保留当前批次原有唯一约束，旧版应用回退后仍可使用原写入语句。
CREATE TABLE filing_precheck_history
    (LIKE filing_prechecks INCLUDING CONSTRAINTS);
ALTER TABLE filing_precheck_history ADD PRIMARY KEY (id);
CREATE INDEX filing_precheck_history_recent
    ON filing_precheck_history(filing_id, created_at DESC, id DESC);
