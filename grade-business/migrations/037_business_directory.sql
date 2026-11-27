CREATE TABLE directory_parties (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind text NOT NULL CHECK (kind IN ('unit', 'institution')),
    scope_name text NOT NULL CHECK (length(scope_name) BETWEEN 1 AND 200),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 80),
    classification text NOT NULL DEFAULT '',
    district text NOT NULL DEFAULT '',
    qualification text NOT NULL DEFAULT '',
    valid_until date,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    owner_institution_id uuid,
    owner_kind text GENERATED ALWAYS AS ('institution') STORED,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, kind),
    FOREIGN KEY (owner_institution_id, owner_kind) REFERENCES directory_parties(id, kind),
    CHECK ((kind='unit' AND code ~ '^[0-9A-Z]{18}$' AND qualification='' AND valid_until IS NULL)
        OR (kind='institution' AND qualification<>'' AND valid_until IS NOT NULL
            AND owner_institution_id IS NULL))
);

CREATE UNIQUE INDEX directory_parties_kind_scope ON directory_parties (kind, lower(scope_name));
CREATE UNIQUE INDEX directory_parties_kind_name ON directory_parties (kind, lower(name));
CREATE UNIQUE INDEX directory_parties_kind_code ON directory_parties (kind, lower(code));
CREATE INDEX directory_parties_list ON directory_parties (kind, name COLLATE "C", id);

CREATE TABLE directory_people (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id uuid NOT NULL,
    institution_kind text GENERATED ALWAYS AS ('institution') STORED,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 80),
    certificate_no text NOT NULL DEFAULT '',
    qualification text NOT NULL DEFAULT '',
    valid_until date,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (institution_id, institution_kind) REFERENCES directory_parties (id, kind)
);

CREATE UNIQUE INDEX directory_people_institution_code
    ON directory_people (institution_id, lower(code));
CREATE UNIQUE INDEX directory_people_certificate
    ON directory_people (lower(certificate_no)) WHERE certificate_no<>'';
CREATE INDEX directory_people_list ON directory_people (name COLLATE "C", id);

CREATE TABLE directory_unit_memberships (
    institution_id uuid NOT NULL,
    institution_kind text GENERATED ALWAYS AS ('institution') STORED,
    unit_id uuid NOT NULL,
    unit_kind text GENERATED ALWAYS AS ('unit') STORED,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (institution_id, unit_id),
    FOREIGN KEY (institution_id, institution_kind) REFERENCES directory_parties(id, kind),
    FOREIGN KEY (unit_id, unit_kind) REFERENCES directory_parties(id, kind)
);

CREATE INDEX directory_unit_memberships_unit ON directory_unit_memberships (unit_id, institution_id);

CREATE TABLE directory_events (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_kind text NOT NULL CHECK (entry_kind IN ('unit', 'institution', 'person')),
    entry_id uuid NOT NULL,
    entry_version bigint NOT NULL CHECK (entry_version > 0),
    action text NOT NULL CHECK (action IN ('create', 'update', 'disable', 'enable', 'transfer', 'link')),
    actor text NOT NULL,
    institution_id uuid REFERENCES directory_parties(id),
    before_values jsonb,
    after_values jsonb NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (jsonb_typeof(after_values)='object'),
    CHECK (before_values IS NULL OR jsonb_typeof(before_values)='object')
);

CREATE INDEX directory_events_entry ON directory_events (entry_kind, entry_id, sequence DESC);

ALTER TABLE business_cases
    ADD COLUMN directory_unit_id uuid REFERENCES directory_parties(id),
    ADD COLUMN directory_institution_id uuid REFERENCES directory_parties(id),
    ADD COLUMN directory_person_id uuid REFERENCES directory_people(id),
    ADD COLUMN directory_snapshot jsonb;

ALTER TABLE implementation_reports
    ADD COLUMN directory_unit_id uuid REFERENCES directory_parties(id),
    ADD COLUMN directory_institution_id uuid REFERENCES directory_parties(id),
    ADD COLUMN directory_person_id uuid REFERENCES directory_people(id),
    ADD COLUMN directory_snapshot jsonb;

ALTER TABLE case_review_events ADD COLUMN directory_snapshot jsonb;
ALTER TABLE implementation_events ADD COLUMN directory_snapshot jsonb;

CREATE INDEX business_cases_directory_unit ON business_cases(directory_unit_id)
    WHERE directory_unit_id IS NOT NULL;
CREATE INDEX business_cases_directory_institution ON business_cases(directory_institution_id)
    WHERE directory_institution_id IS NOT NULL;
CREATE INDEX business_cases_directory_person ON business_cases(directory_person_id)
    WHERE directory_person_id IS NOT NULL;
CREATE INDEX implementation_reports_directory_unit ON implementation_reports(directory_unit_id)
    WHERE directory_unit_id IS NOT NULL;
CREATE INDEX implementation_reports_directory_institution ON implementation_reports(directory_institution_id)
    WHERE directory_institution_id IS NOT NULL;
CREATE INDEX implementation_reports_directory_person ON implementation_reports(directory_person_id)
    WHERE directory_person_id IS NOT NULL;
