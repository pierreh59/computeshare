-- ComputeShare - Migration 007 : billing credits clients B2B (Sprint 13)
-- LAI-32 : prepaidement en credits, facturation a la GPU-heure reelle,
--           TVA (OSS : 20 % pour les clients francais, autoliquidation
--           pour les clients UE entreprises), rapports d'usage,
--           remboursements.

-- Journal de billing par organisation (append-only, comme le ledger
-- vendeur : chaque evenement est une ecriture immuable).
CREATE TABLE runtime_billing_events (
    id         BIGSERIAL PRIMARY KEY,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    org_id     TEXT        NOT NULL,
    event_type TEXT        NOT NULL, -- topup | usage | refund | alert
    amount_eur NUMERIC(12,6) NOT NULL, -- credit > 0, debit < 0
    vat_eur    NUMERIC(12,6) NOT NULL DEFAULT 0,
    gpu_hours  NUMERIC(12,6) NOT NULL DEFAULT 0,
    ref        TEXT        NOT NULL DEFAULT '' -- chunk_id ou job_id
);
CREATE INDEX idx_runtime_billing_events_org ON runtime_billing_events (org_id, at);

-- Solde de credits par organisation (denormalise pour la reactivite ;
-- la source de verite reste le journal ci-dessus).
CREATE TABLE runtime_billing_balances (
    org_id     TEXT PRIMARY KEY,
    balance_eur NUMERIC(12,6) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Le chunk connait son organisation cliente (pour la facturation apres
-- un redemarrage de l'orchestrateur).
ALTER TABLE runtime_chunks ADD COLUMN org_id TEXT NOT NULL DEFAULT '';
