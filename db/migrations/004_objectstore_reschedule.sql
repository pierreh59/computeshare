-- ComputeShare - Migration 004 : fiabilite + assets (Sprint 10)
-- LAI-27 : suivi des tentatives de chunks (re-scheduling).
-- LAI-28 : object store des assets clients + zones (data-locality).

ALTER TABLE runtime_chunks ADD COLUMN attempts SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE runtime_chunks ADD COLUMN zone TEXT;
ALTER TABLE runtime_chunks ADD COLUMN assets TEXT NOT NULL DEFAULT '[]';

ALTER TABLE runtime_nodes ADD COLUMN zone TEXT NOT NULL DEFAULT 'local';

CREATE TABLE runtime_assets (
  id          TEXT PRIMARY KEY,
  org_id      TEXT NOT NULL REFERENCES runtime_orgs(id),
  filename    TEXT NOT NULL,
  sha256      TEXT NOT NULL,
  size_bytes  BIGINT NOT NULL,
  zone        TEXT NOT NULL DEFAULT 'local',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtime_assets_org ON runtime_assets (org_id, created_at DESC);
