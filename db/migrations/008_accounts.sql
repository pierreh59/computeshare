-- ComputeShare - Migration 008 : comptes utilisateurs (Sprint 15)
-- LAI-57 : inscription libre des clients (besoin de puissance) et des
-- vendeurs (proposent leur puissance). Un compte client est lie a une
-- organisation (credits + cle API B2B) ; un compte vendeur possede un
-- jeton d'enrolement personnel (remplace le jeton partage de la beta
-- ferme pour les comptes crees via inscription).

CREATE TABLE runtime_users (
    id                TEXT PRIMARY KEY,
    email             TEXT NOT NULL UNIQUE,
    password_hash     TEXT NOT NULL,             -- bcrypt
    role              TEXT NOT NULL,             -- client | seller
    display_name      TEXT NOT NULL DEFAULT '',
    org_id            TEXT NOT NULL DEFAULT '', -- role client : organisation liee
    seller_token_hash TEXT NOT NULL DEFAULT '', -- role vendeur : SHA-256 du jeton personnel
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtime_users_email ON runtime_users (email);

-- Le noeud connait son compte vendeur (console vendeur, retraits KYC).
ALTER TABLE runtime_nodes ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
