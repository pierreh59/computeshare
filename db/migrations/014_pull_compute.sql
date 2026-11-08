-- ComputeShare - Migration 014 : distinction temps pull / temps de
-- calcul (Sprint 30).
-- Le noeud rapporte desormais pull_ms (telechargement de l'image
-- Docker chez le fournisseur) et compute_ms (execution reelle).
-- Le vendeur est paye sur le temps de calcul uniquement ; le client
-- paie le calcul a plein tarif et le pull a 20 % du tarif.

ALTER TABLE runtime_chunks
    ADD COLUMN IF NOT EXISTS pull_ms BIGINT NOT NULL DEFAULT 0;
ALTER TABLE runtime_chunks
    ADD COLUMN IF NOT EXISTS compute_ms BIGINT NOT NULL DEFAULT 0;
