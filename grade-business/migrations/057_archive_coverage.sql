CREATE TABLE archive_coverage_targets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    unit_id uuid NOT NULL,
    unit_kind text GENERATED ALWAYS AS ('unit') STORED,
    filing_id uuid REFERENCES business_cases(id),
    system_name text NOT NULL CHECK (length(system_name) BETWEEN 1 AND 200),
    reference text NOT NULL CHECK (length(reference) BETWEEN 1 AND 120),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 300),
    active boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (unit_id,unit_kind) REFERENCES directory_parties(id,kind)
);

CREATE UNIQUE INDEX archive_coverage_reference ON archive_coverage_targets (lower(reference));
CREATE UNIQUE INDEX archive_coverage_filing ON archive_coverage_targets (filing_id)
    WHERE active AND filing_id IS NOT NULL;
CREATE INDEX archive_coverage_active_unit ON archive_coverage_targets (unit_id) WHERE active;
CREATE INDEX archive_coverage_list ON archive_coverage_targets (created_at DESC,id DESC);

CREATE VIEW archive_coverage_status AS
 SELECT t.*,u.name AS unit_name,u.district,
        COALESCE(f.number,'') AS filing_number,
        (t.active AND f.id IS NOT NULL AND f.kind='filing' AND f.status='approved'
         AND f.cancelled_at IS NULL AND f.discarded_at IS NULL AND f.system_name=t.system_name
         AND COALESCE(f.directory_unit_id=t.unit_id,f.organization_name IN (u.scope_name,u.name))) AS covered
 FROM archive_coverage_targets t JOIN directory_parties u ON u.id=t.unit_id
 LEFT JOIN business_cases f ON f.id=t.filing_id;

CREATE TRIGGER archive_coverage_targets_event
AFTER INSERT OR UPDATE ON archive_coverage_targets
FOR EACH ROW EXECUTE FUNCTION record_archive_registration('coverage-targets');
