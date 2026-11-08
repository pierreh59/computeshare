-- ComputeShare - Migration 013 : verification email + reinitialisation
-- du mot de passe (Sprint 29).
-- Un compte a l'adresse non verifiee est LIMITE (pas de soumission de
-- job) : le lien de verification est envoye par e-mail et valable 1 h.

ALTER TABLE runtime_users
    ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS auth_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL,
    kind       TEXT NOT NULL,          -- verify_email | reset_password
    token_hash TEXT NOT NULL UNIQUE,   -- SHA-256 hex du jeton brut
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens (user_id, kind);
