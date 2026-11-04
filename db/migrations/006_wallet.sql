-- ComputeShare - Migration 006 : wallet & ledger des vendeurs (Sprint 12)
-- LAI-34 : ledger double-entrée immuable (insertion seule, jamais
--           d'UPDATE ni de DELETE depuis le code applicatif).
-- LAI-35 : retraits (gift cards x3 + PayPal), seuil 5 EUR, frais
--           affiches avant confirmation.
-- LAI-36 : KYC vendeur obligatoire avant le premier retrait.

-- Grand livre : chaque mouvement est une ecriture signee
-- (credit > 0 = gain, debit < 0 = retrait ou frais).
CREATE TABLE runtime_ledger (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    node_id    TEXT        NOT NULL,
    entry_type TEXT        NOT NULL,  -- payout | withdrawal | fee
    amount_eur NUMERIC(12,6) NOT NULL,
    ref        TEXT        NOT NULL DEFAULT ''   -- chunk_id ou withdrawal_id
);
CREATE INDEX idx_runtime_ledger_node ON runtime_ledger (node_id, at);

-- Retraits demandes par les vendeurs.
CREATE TABLE runtime_withdrawals (
    id         TEXT PRIMARY KEY,
    node_id    TEXT        NOT NULL,
    provider   TEXT        NOT NULL,  -- paypal | gift_card_amazon | gift_card_steam | gift_card_googleplay
    amount_eur NUMERIC(12,6) NOT NULL,
    fee_eur    NUMERIC(12,6) NOT NULL DEFAULT 0,
    status     TEXT        NOT NULL DEFAULT 'pending',  -- pending | processed | failed
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_runtime_withdrawals_node ON runtime_withdrawals (node_id);

-- Statut KYC par vendeur (LAI-36).
CREATE TABLE runtime_kyc (
    node_id    TEXT PRIMARY KEY,
    status     TEXT        NOT NULL DEFAULT 'none',  -- none | pending | approved | rejected
    checked_at TIMESTAMPTZ
);
