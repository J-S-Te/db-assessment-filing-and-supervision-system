ALTER TABLE business_cases ADD COLUMN classification_assessment jsonb
    CHECK (classification_assessment IS NULL OR
           (kind = 'filing' AND jsonb_typeof(classification_assessment) = 'object'));
