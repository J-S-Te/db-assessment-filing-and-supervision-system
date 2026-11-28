ALTER TABLE directory_parties
    DROP CONSTRAINT directory_parties_check,
    ADD CONSTRAINT directory_parties_check
        CHECK ((kind='unit' AND code ~ '^[0-9A-Z]{18}$' AND qualification='' AND valid_until IS NULL)
            OR (kind='institution' AND valid_until IS NOT NULL AND owner_institution_id IS NULL));
