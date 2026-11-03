-- ComputeShare - Migration 003 : API B2B (Sprint 9, LAI-29)
-- Organisations clientes + cles API (MVP runtime).
-- La cle est stockee en SHA-256, jamais en clair.

CREATE TABLE runtime_orgs (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  api_key_hash  TEXT NOT NULL UNIQUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Le statut "canceled" rejoint les statuts possibles des chunks.
ALTER TABLE runtime_chunks DROP CONSTRAINT runtime_chunks_status_check;
ALTER TABLE runtime_chunks ADD CONSTRAINT runtime_chunks_status_check
  CHECK (status IN ('queued','running','done','failed','canceled'));
