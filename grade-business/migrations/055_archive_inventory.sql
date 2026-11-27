CREATE TABLE archive_system_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    filing_id uuid NOT NULL REFERENCES business_cases(id),
    kind text NOT NULL CHECK (kind IN ('ip','domain','topology','supplier','security_event','cloud','big_data')),
    identifier text NOT NULL CHECK (length(identifier) BETWEEN 1 AND 253),
    related_endpoint text NOT NULL DEFAULT '' CHECK (length(related_endpoint) <= 253),
    label text NOT NULL DEFAULT '' CHECK (length(label) <= 200),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 300),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    occurred_on date,
    active boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (filing_id, kind, identifier, related_endpoint),
    CHECK ((kind='topology' AND related_endpoint<>'' AND identifier<>related_endpoint)
        OR (kind<>'topology' AND related_endpoint='')),
    CHECK ((kind='security_event') = (occurred_on IS NOT NULL))
);

CREATE INDEX archive_system_assets_list ON archive_system_assets (filing_id, created_at DESC, id DESC);
CREATE INDEX archive_system_assets_active ON archive_system_assets (filing_id, kind) WHERE active;

CREATE TABLE archive_registration_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    resource text NOT NULL CHECK (resource IN ('system-assets','coverage-targets')),
    record_id uuid NOT NULL,
    version bigint NOT NULL,
    before_values jsonb,
    after_values jsonb NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (resource, record_id, version)
);

CREATE FUNCTION record_archive_registration() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO archive_registration_events(resource,record_id,version,before_values,after_values)
    VALUES (TG_ARGV[0],NEW.id,NEW.version,CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) END,to_jsonb(NEW));
    RETURN NEW;
END;
$$;

CREATE TRIGGER archive_system_assets_event
AFTER INSERT OR UPDATE ON archive_system_assets
FOR EACH ROW EXECUTE FUNCTION record_archive_registration('system-assets');
