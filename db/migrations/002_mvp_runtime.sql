-- ComputeShare - Migration 002 : persistance runtime MVP (Sprint 8)
-- Ces tables refletent le modele runtime actuel de l'orchestrateur
-- (identifiants libres, avant auth users/orgs). Elles seront
-- rapprochees du schema core (001) lors de l'integration des comptes
-- (LAI-29 / LAI-39).

CREATE TABLE runtime_nodes (
  id            TEXT PRIMARY KEY,
  gpu_model     TEXT NOT NULL,
  gpu_category  TEXT NOT NULL REFERENCES gpu_categories(id),
  trust_score   NUMERIC(5,2) NOT NULL DEFAULT 100.00,
  status        TEXT NOT NULL DEFAULT 'active',
  last_seen     TIMESTAMPTZ NOT NULL DEFAULT now(),
  online        BOOLEAN NOT NULL DEFAULT true,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE runtime_chunks (
  id            TEXT PRIMARY KEY,
  job_id        TEXT NOT NULL,
  node_id       TEXT NOT NULL REFERENCES runtime_nodes(id),
  command       TEXT NOT NULL,
  args          TEXT NOT NULL DEFAULT '[]',  -- tableau JSON
  expected_output TEXT NOT NULL,
  timeout_secs  BIGINT NOT NULL DEFAULT 300,
  status        TEXT NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued','running','done','failed')),
  result_sha256 TEXT,
  price_eur_per_hour NUMERIC(10,4) NOT NULL DEFAULT 0,
  payout_eur    NUMERIC(12,6) NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtime_chunks_status ON runtime_chunks (status);

CREATE TABLE runtime_price_history (
  category     TEXT NOT NULL REFERENCES gpu_categories(id),
  eur_per_hour NUMERIC(10,4) NOT NULL,
  changed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtime_price_last
  ON runtime_price_history (category, changed_at DESC);
