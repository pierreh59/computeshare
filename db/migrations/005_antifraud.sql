-- ComputeShare - Migration 005 : anti-fraude & preuve d'execution (Sprint 11)
-- LAI-43 : sampling des chunks termines (audit futur) + duree reelle.
-- LAI-44 : empreinte machine des noeuds (anti-Sybil), trust score
--           deja present dans runtime_nodes.

ALTER TABLE runtime_chunks ADD COLUMN duration_secs BIGINT NOT NULL DEFAULT 0;
ALTER TABLE runtime_chunks ADD COLUMN sampled BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_runtime_chunks_sampled ON runtime_chunks (sampled) WHERE sampled;

ALTER TABLE runtime_nodes ADD COLUMN machine_fingerprint TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_runtime_nodes_fingerprint ON runtime_nodes (machine_fingerprint);
