-- ComputeShare - Migration 016 : persistance des chunks conteneurs
-- (Sprint 35). Bug reel du 11 nov. 2026 : la table runtime_chunks ne
-- gardait ni le runtime ("container"), ni l'image Docker, ni le flag
-- GPU. Apres un redemarrage de l'orchestrateur, tout chunk non
-- termine recharge perdait son image : le nœud le refusait
-- ("rejected - commande hors allowlist") et le job echouait.

ALTER TABLE runtime_chunks
    ADD COLUMN IF NOT EXISTS runtime TEXT NOT NULL DEFAULT '';

ALTER TABLE runtime_chunks
    ADD COLUMN IF NOT EXISTS image TEXT NOT NULL DEFAULT '';

ALTER TABLE runtime_chunks
    ADD COLUMN IF NOT EXISTS gpu BOOLEAN NOT NULL DEFAULT FALSE;
