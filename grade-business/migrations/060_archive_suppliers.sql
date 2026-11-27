CREATE TABLE archive_suppliers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    service_type text NOT NULL CHECK (length(service_type) BETWEEN 1 AND 120),
    unit_id uuid,
    unit_kind text GENERATED ALWAYS AS ('unit') STORED,
    started_on date,
    contact_name text NOT NULL DEFAULT '' CHECK (length(contact_name) <= 120),
    contact_phone text NOT NULL DEFAULT '' CHECK (length(contact_phone) <= 80),
    source text NOT NULL CHECK (length(source) BETWEEN 1 AND 300),
    active boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (unit_id,unit_kind) REFERENCES directory_parties(id,kind)
);

CREATE UNIQUE INDEX archive_suppliers_name ON archive_suppliers (lower(name));
CREATE INDEX archive_suppliers_list ON archive_suppliers (created_at DESC,id DESC);
CREATE INDEX archive_suppliers_unit ON archive_suppliers (unit_id) WHERE unit_id IS NOT NULL;

ALTER TABLE archive_system_assets ADD COLUMN supplier_id uuid REFERENCES archive_suppliers(id),
    ADD CONSTRAINT archive_system_assets_supplier_kind CHECK (supplier_id IS NULL OR kind='supplier');
CREATE UNIQUE INDEX archive_system_assets_supplier_unique ON archive_system_assets (filing_id,supplier_id)
    WHERE supplier_id IS NOT NULL;
CREATE INDEX archive_system_assets_supplier_list ON archive_system_assets (supplier_id,created_at DESC,id DESC)
    WHERE supplier_id IS NOT NULL;
CREATE INDEX archive_system_assets_topology_list ON archive_system_assets (filing_id,created_at DESC,id DESC)
    WHERE kind='topology';

ALTER TABLE archive_registration_events DROP CONSTRAINT archive_registration_events_resource_check;
ALTER TABLE archive_registration_events ADD CHECK (resource IN ('system-assets','coverage-targets','suppliers'));
CREATE TRIGGER archive_suppliers_event
AFTER INSERT OR UPDATE ON archive_suppliers
FOR EACH ROW EXECUTE FUNCTION record_archive_registration('suppliers');
