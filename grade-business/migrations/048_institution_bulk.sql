-- 删除从未送审的机构草稿时保留原记录及材料。
ALTER TABLE business_cases DROP CONSTRAINT business_cases_discarded_draft,
    ADD CONSTRAINT business_cases_discarded_draft CHECK (
        discarded_at IS NULL OR (status='draft' AND submitted_at IS NULL
            AND (kind='report' OR (kind='filing' AND NOT operator_origin AND cancelled_at IS NULL)))
    );

-- 操作与结果一起提交，避免同一机构重复执行已完成的批次。
CREATE TABLE institution_filing_commands (
    institution text NOT NULL CHECK (length(btrim(institution)) BETWEEN 1 AND 200),
    request_id uuid NOT NULL,
    command text NOT NULL CHECK (command IN ('copy','submit','discard')),
    input_digest text NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
    result jsonb CHECK (jsonb_typeof(result)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (institution,request_id)
);
