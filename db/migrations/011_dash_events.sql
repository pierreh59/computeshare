-- ComputeShare - Migration 011 : observabilite (Sprint 24)
-- Journal d'evenements persiste : le dashboard admin garde son
-- historique a travers les redemarrages du serveur (le ring buffer
-- memoire se vidait a chaque deploy). Idempotent (IF NOT EXISTS).

CREATE TABLE IF NOT EXISTS dash_events (
    id  BIGSERIAL PRIMARY KEY,
    at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    type TEXT       NOT NULL, -- node_register | job_submit | chunk_done | ...
    msg  TEXT       NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dash_events_at ON dash_events (at DESC);
