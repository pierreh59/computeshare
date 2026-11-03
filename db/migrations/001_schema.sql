-- ComputeShare - Migration 001 : schema core (LAI-17)
-- PostgreSQL 15+

CREATE TABLE gpu_categories (
  id          TEXT PRIMARY KEY,
  label       TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO gpu_categories (id, label) VALUES
  ('consumer', 'Consumer (RTX 3060)'),
  ('prosumer', 'Prosumer (RTX 4070/3080)'),
  ('high_end', 'High-end (RTX 3090/4090)');

CREATE TABLE users (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email       TEXT NOT NULL UNIQUE,
  display_name TEXT,
  role        TEXT NOT NULL CHECK (role IN ('provider', 'client_member')),
  country     TEXT,
  locale      TEXT NOT NULL DEFAULT 'fr',
  tos_accepted_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at  TIMESTAMPTZ
);

CREATE TABLE organizations (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT NOT NULL,
  billing_country TEXT,
  vat_number  TEXT,
  credit_balance_cents BIGINT NOT NULL DEFAULT 0 CHECK (credit_balance_cents >= 0),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE org_members (
  org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        TEXT NOT NULL CHECK (role IN ('admin', 'developer', 'billing')),
  PRIMARY KEY (org_id, user_id)
);

CREATE TABLE nodes (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id),
  machine_fingerprint TEXT NOT NULL,
  gpu_model     TEXT NOT NULL,
  gpu_category  TEXT NOT NULL REFERENCES gpu_categories(id),
  vram_mb       INTEGER NOT NULL,
  os            TEXT NOT NULL,
  country       TEXT,
  benchmark_score NUMERIC(10,2),
  trust_score   NUMERIC(5,2) NOT NULL DEFAULT 100.00,
  status        TEXT NOT NULL DEFAULT 'pending_benchmark'
                CHECK (status IN ('pending_benchmark','active','suspended','revoked')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, machine_fingerprint)
);
CREATE INDEX idx_nodes_status_cat ON nodes (status, gpu_category);

CREATE TABLE node_presence (
  node_id       UUID PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  last_heartbeat TIMESTAMPTZ NOT NULL,
  gpu_load_pct  NUMERIC(5,2),
  intensity     NUMERIC(4,3),
  online        BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE jobs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        UUID NOT NULL REFERENCES organizations(id),
  created_by    UUID NOT NULL REFERENCES users(id),
  title         TEXT,
  image         TEXT NOT NULL,
  gpu_category  TEXT NOT NULL REFERENCES gpu_categories(id),
  gpu_hours     NUMERIC(10,2) NOT NULL,
  interruptible BOOLEAN NOT NULL DEFAULT false,
  zone          TEXT,
  status        TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued','running','completed','failed','canceled')),
  budget_cents  BIGINT NOT NULL,
  spend_cents   BIGINT NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at    TIMESTAMPTZ,
  finished_at   TIMESTAMPTZ
);
CREATE INDEX idx_jobs_status ON jobs (status, gpu_category);

CREATE TABLE chunks (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id        UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  status        TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued','assigned','running','done','failed','lost')),
  assigned_node UUID REFERENCES nodes(id),
  attempts      SMALLINT NOT NULL DEFAULT 0,
  checksum      TEXT,
  verified      BOOLEAN NOT NULL DEFAULT false,
  started_at    TIMESTAMPTZ,
  done_at       TIMESTAMPTZ,
  UNIQUE (job_id, seq)
);
CREATE INDEX idx_chunks_assignable ON chunks (status) WHERE status = 'queued';

-- Pricing dynamique (ADR-006) : prix versionnes, jamais modifies en place.
-- Le payout d'un chunk est gele au tarif en vigueur a son execution (LAI-52).
CREATE TABLE price_history (
  id              BIGSERIAL PRIMARY KEY,
  gpu_category    TEXT NOT NULL REFERENCES gpu_categories(id),
  price_per_hour  NUMERIC(10,4) NOT NULL,
  pay_rate_per_hour NUMERIC(10,4) NOT NULL,
  min_pay_rate    NUMERIC(10,4) NOT NULL,
  set_by          TEXT NOT NULL DEFAULT 'admin',
  reason          TEXT,
  valid_from      TIMESTAMPTZ NOT NULL DEFAULT now(),
  valid_to        TIMESTAMPTZ
);
CREATE INDEX idx_price_active ON price_history (gpu_category) WHERE valid_to IS NULL;

INSERT INTO price_history (gpu_category, price_per_hour, pay_rate_per_hour, min_pay_rate) VALUES
  ('consumer', 0.28, 0.17, 0.15),
  ('prosumer', 0.42, 0.25, 0.20),
  ('high_end', 0.68, 0.42, 0.35);

CREATE TABLE ledger_entries (
  id            BIGSERIAL PRIMARY KEY,
  journal       TEXT NOT NULL CHECK (journal IN ('provider_earnings', 'client_spend', 'payout', 'adjustment')),
  user_id       UUID REFERENCES users(id),
  org_id        UUID REFERENCES organizations(id),
  node_id       UUID REFERENCES nodes(id),
  chunk_id      UUID REFERENCES chunks(id),
  amount_cents  BIGINT NOT NULL,
  balance_after_cents BIGINT NOT NULL,
  currency      TEXT NOT NULL DEFAULT 'EUR',
  memo          TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ledger_user ON ledger_entries (user_id, created_at);
CREATE INDEX idx_ledger_org  ON ledger_entries (org_id, created_at);

CREATE TABLE payouts (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id),
  method        TEXT NOT NULL CHECK (method IN ('gift_card','paypal','sepa','usdc')),
  reference     TEXT,
  amount_cents  BIGINT NOT NULL CHECK (amount_cents > 0),
  status        TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','kyc_hold','processing','sent','failed')),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at       TIMESTAMPTZ
);

CREATE TABLE image_scans (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id        UUID REFERENCES jobs(id),
  image         TEXT NOT NULL,
  verdict       TEXT NOT NULL CHECK (verdict IN ('clean','suspicious','blocked')),
  details       JSONB,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE VIEW v_ledger_reconciliation AS
SELECT date_trunc('day', created_at) AS day, journal,
       sum(amount_cents) AS total_cents, count(*) AS entries
FROM ledger_entries
GROUP BY 1, 2
ORDER BY 1 DESC;
