CREATE TABLE access_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,63}$'),
    display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
    role_code text NOT NULL,
    person_key text UNIQUE,
    area_code text,
    subject_id uuid UNIQUE,
    subject_kind text GENERATED ALWAYS AS (
        CASE role_code WHEN 'ROLE_ORG_ADMIN' THEN 'institution'
                       WHEN 'ROLE_OPERATOR' THEN 'unit' END
    ) STORED,
    password_salt bytea NOT NULL CHECK (octet_length(password_salt)=16),
    password_hash bytea NOT NULL CHECK (octet_length(password_hash)=32),
    must_change_password boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (subject_id, subject_kind) REFERENCES directory_parties(id, kind),
    CHECK (
        (role_code='ROLE_SYS_ADMIN' AND person_key IS NOT NULL AND area_code IS NULL AND subject_id IS NULL)
        OR (role_code='ROLE_REGULATOR' AND person_key IS NOT NULL AND area_code IS NOT NULL
            AND area_code IN ('331000','331002','331003','331004','331022','331023',
                             '331024','331081','331082','331083') AND subject_id IS NULL)
        OR (role_code IN ('ROLE_ORG_ADMIN','ROLE_OPERATOR') AND person_key IS NULL
            AND area_code IS NULL AND subject_id IS NOT NULL)
    )
);

CREATE TABLE access_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES access_accounts(id),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX access_sessions_account ON access_sessions(account_id);
CREATE INDEX access_sessions_expiry ON access_sessions(expires_at);

CREATE TABLE access_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id uuid REFERENCES access_accounts(id),
    role_code text,
    event text NOT NULL,
    target text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX access_events_operator ON access_events(operator_id, occurred_at DESC);
