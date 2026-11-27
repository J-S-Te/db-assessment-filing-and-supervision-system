ALTER TABLE publication_events
    DROP CONSTRAINT publication_events_category_check,
    ADD CONSTRAINT publication_events_category_check
        CHECK (category IN ('bulletin','security_advisory','announcement','policy_standard','common_material'));
