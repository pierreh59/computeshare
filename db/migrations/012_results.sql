-- Sprint 26 : rapatriement des fichiers de resultat.
-- Un noeud qui termine un chunk pousse son fichier de sortie vers le
-- serveur (POST /results), le client le recupere (GET /v1/results/...).
-- Purge automatique apres 30 jours (resultRetention).

CREATE TABLE IF NOT EXISTS runtime_results (
    chunk_id   TEXT PRIMARY KEY,
    job_id     TEXT NOT NULL DEFAULT '',
    org_id     TEXT NOT NULL DEFAULT '',
    file_name  TEXT NOT NULL DEFAULT 'result.bin',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    sha256     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_results_created ON runtime_results (created_at);
