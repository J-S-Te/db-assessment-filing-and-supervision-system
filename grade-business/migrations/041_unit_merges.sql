CREATE TABLE directory_unit_merges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id uuid NOT NULL REFERENCES directory_parties(id),
    preview_token text NOT NULL UNIQUE CHECK (preview_token ~ '^[0-9a-f]{64}$'),
    plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object'),
    actor text NOT NULL,
    merged_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE directory_parties
    ADD COLUMN merged_into_id uuid,
    ADD COLUMN merge_id uuid REFERENCES directory_unit_merges(id),
    ADD COLUMN merged_at timestamptz,
    ADD FOREIGN KEY (merged_into_id,kind) REFERENCES directory_parties(id,kind),
    ADD CHECK ((merged_into_id IS NULL AND merge_id IS NULL AND merged_at IS NULL) OR
              (merged_into_id IS NOT NULL AND merge_id IS NOT NULL AND merged_at IS NOT NULL AND
               id<>merged_into_id AND kind='unit' AND status='inactive'));

CREATE INDEX directory_parties_merged_into ON directory_parties(merged_into_id)
    WHERE merged_into_id IS NOT NULL;

ALTER TABLE directory_events DROP CONSTRAINT directory_events_action_check;
ALTER TABLE directory_events ADD CONSTRAINT directory_events_action_check
    CHECK (action IN ('create','update','disable','enable','transfer','link','merge'));

CREATE FUNCTION reject_filing_for_merged_unit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind <> 'filing' THEN
        RETURN NEW;
    END IF;
    IF TG_OP='UPDATE' THEN
        IF NEW.organization_name=OLD.organization_name
           AND NEW.directory_unit_id IS NOT DISTINCT FROM OLD.directory_unit_id THEN
            RETURN NEW;
        END IF;
    END IF;
    IF EXISTS (
        SELECT 1 FROM directory_parties u WHERE u.kind='unit' AND u.merged_into_id IS NOT NULL
          AND (u.id=NEW.directory_unit_id
               OR lower(btrim(NEW.organization_name)) IN (lower(u.scope_name),lower(u.name)))
    ) THEN
        RAISE check_violation USING
            MESSAGE='cannot start a filing for a merged unit',
            CONSTRAINT='filing_unit_merged';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER filing_unit_merge_guard
    BEFORE INSERT OR UPDATE OF organization_name,directory_unit_id ON business_cases
    FOR EACH ROW EXECUTE FUNCTION reject_filing_for_merged_unit();
