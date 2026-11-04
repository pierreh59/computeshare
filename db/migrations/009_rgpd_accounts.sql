-- ComputeShare - Migration 009 : conformite RGPD des comptes (Sprint 15c)
-- LAI-60 : consentement horodate a l'inscription, revocation des
-- sessions (epoque), suppression/anonymisation du compte (droit a
-- l'effacement). Les ecritures comptables (billing, wallet) restent
-- conservees anonymisees : obligation legale de conservation.

ALTER TABLE runtime_users ADD COLUMN IF NOT EXISTS consent_at TIMESTAMPTZ;
ALTER TABLE runtime_users ADD COLUMN IF NOT EXISTS sessions_before TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0);
ALTER TABLE runtime_users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
