# ComputeShare

Marketplace de calcul GPU distribué grand public (type Salad.com).

## Structure

- apps/desktop - app fournisseur (Tauri 2 / Rust)
- apps/web - web fournisseur + console B2B (React + Vite + Tailwind)
- services/orchestrator - gateway noeuds, chunker, scheduler (Go)
- packages/sdk - SDK client Python + TypeScript
- db/migrations - migrations PostgreSQL
- infra/ - docker-compose, deploiement

## Docs

- docs/ADR.md - decisions techniques (stack, sandbox, pricing dynamique)
- Backlog : projet Linear ComputeShare (8 epics, 41 tickets)

## Developpement local

docker compose -f infra/docker-compose.yml up -d
cd services/orchestrator; go run main.go
