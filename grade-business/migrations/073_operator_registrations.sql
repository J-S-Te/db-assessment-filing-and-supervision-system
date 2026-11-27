CREATE TABLE operator_registrations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    unit_name text NOT NULL CHECK (length(unit_name) BETWEEN 1 AND 200),
    credit_code text NOT NULL CHECK (credit_code ~ '^[0-9A-HJ-NP-RTUWXY]{18}$'),
    contact_name text NOT NULL CHECK (length(contact_name) BETWEEN 1 AND 100),
    phone text NOT NULL CHECK (phone ~ '^1[3-9][0-9]{9}$'),
    username text NOT NULL CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    area_code text NOT NULL CHECK (area_code IN
        ('331000','331002','331003','331004','331022','331023','331024','331081','331082','331083')),
    original_unit_id uuid REFERENCES directory_parties(id),
    lookup_hash bytea NOT NULL UNIQUE CHECK (octet_length(lookup_hash)=32),
    password_salt bytea CHECK (octet_length(password_salt)=16),
    password_hash bytea CHECK (octet_length(password_hash)=32),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reviewed_at timestamptz,
    reviewer_id uuid REFERENCES access_accounts(id),
    reviewer_role text CHECK (reviewer_role IN ('ROLE_SYS_ADMIN','ROLE_REGULATOR')),
    reviewer_name text,
    reason text NOT NULL DEFAULT '' CHECK (length(reason)<=500),
    account_id uuid UNIQUE REFERENCES access_accounts(id),
    subject_id uuid REFERENCES directory_parties(id),
    CHECK (
        (status='pending' AND password_salt IS NOT NULL AND password_hash IS NOT NULL
          AND reviewed_at IS NULL AND reviewer_id IS NULL AND reviewer_role IS NULL
          AND reviewer_name IS NULL AND reason='' AND account_id IS NULL AND subject_id IS NULL)
        OR (status IN ('approved','rejected') AND password_salt IS NULL AND password_hash IS NULL
          AND reviewed_at IS NOT NULL AND reviewer_id IS NOT NULL AND reviewer_role IS NOT NULL
          AND reviewer_name IS NOT NULL
          AND ((status='approved' AND account_id IS NOT NULL AND subject_id IS NOT NULL)
            OR (status='rejected' AND reason<>'' AND account_id IS NULL AND subject_id IS NULL)))
    )
);

CREATE UNIQUE INDEX operator_registrations_open_code
    ON operator_registrations(credit_code) WHERE status IN ('pending','approved');
CREATE INDEX operator_registrations_code_created
    ON operator_registrations(credit_code,created_at);
CREATE INDEX operator_registrations_status_sequence
    ON operator_registrations(status,sequence DESC);
CREATE INDEX operator_registrations_status_area_sequence
    ON operator_registrations(status,area_code,sequence DESC);
