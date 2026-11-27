ALTER TABLE filing_prechecks ADD COLUMN progress_stage text NOT NULL DEFAULT ''
    CHECK (progress_stage IN ('', 'materials', 'deepseek', 'local_rules', 'fallback'));
