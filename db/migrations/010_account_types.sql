-- Sprint 15d : comptes particulier vs societe (SIRET/TVA, LAI-61).
-- Idempotent : chaque ALTER est garde par IF NOT EXISTS ; le service
-- migrate maintient un registre migrations_applied.

ALTER TABLE runtime_users
  ADD COLUMN IF NOT EXISTS account_type TEXT NOT NULL DEFAULT 'particulier',
  ADD COLUMN IF NOT EXISTS company_name TEXT,
  ADD COLUMN IF NOT EXISTS siret TEXT,
  ADD COLUMN IF NOT EXISTS vat_number TEXT,
  ADD COLUMN IF NOT EXISTS company_address TEXT,
  ADD COLUMN IF NOT EXISTS siret_verified BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS siret_checked_at TIMESTAMPTZ;
