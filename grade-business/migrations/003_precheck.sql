CREATE TABLE filing_prechecks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  filing_id uuid NOT NULL,
  revision bigint NOT NULL,
  materials jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed')),
  findings jsonb NOT NULL DEFAULT '[]'::jsonb,
  failure text NOT NULL DEFAULT '',
  attempt integer NOT NULL DEFAULT 0,
  lease_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(filing_id, revision)
);
CREATE INDEX filing_precheck_recent ON filing_prechecks(filing_id, created_at DESC, id DESC);
