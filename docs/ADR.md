# ADR - ComputeShare

## ADR-001 Desktop : Tauri 2 (Rust)
Empreinte ~10 Mo, natif NVML/WMI pour la detection GPU, coeur sandbox en Rust.

## ADR-002 Backend : Go
Gateway + scheduler + API B2B : un seul langage, binaire unique, forte concurrence.

## ADR-003 Donnees : PostgreSQL + NATS JetStream + S3-compatible
Hebergement MVP : VPS + R2 + Postgres manage, moins de 50 EUR/mois avant beta.

## ADR-004 Web : React + Vite + Tailwind
Reutilise le design system du prototype (theme sombre, emerald).

## ADR-005 Sandbox : conteneurs OCI restreints (Podman/WCOW sur Windows)
Risque technique le plus eleve - decoupe en 3 jalons (pull, reseau, limites GPU).

## ADR-006 Pricing : moteur dynamique temps reel
- Prix pilotables en temps reel via console admin ; table price_history versionnee.
- Garde-fous : plancher de reversement par categorie, alerte churn fournisseurs.
- Payout gele au moment de l'execution du chunk - jamais retroactif.

## Juridique
Statut : EI (Pierre). Juriste repousse apres la beta fermee (CGU templates).
