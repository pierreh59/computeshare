-- ComputeShare - Migration 015 : preferences de notifications e-mail
-- (Sprint 31). Deux notifications, desactivables par l utilisateur
-- depuis sa console (case a cocher) :
--   - job termine (reussites / echecs, cout reel)
--   - credits bas (solde sous le seuil de 5 EUR)

ALTER TABLE runtime_users
    ADD COLUMN IF NOT EXISTS notify_job_done BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE runtime_users
    ADD COLUMN IF NOT EXISTS notify_low_credits BOOLEAN NOT NULL DEFAULT true;
