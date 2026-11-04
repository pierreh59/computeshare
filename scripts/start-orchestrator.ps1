# Demarre l'orchestrateur ComputeShare avec les bons parametres locaux.
# Evite le demarrage accidentel en "mode memoire" (variables d'environnement oubliees).
#
# Usage depuis la racine du repo :
#   powershell -ExecutionPolicy Bypass -File .\scripts\start-orchestrator.ps1
# Ou en double-cliquant le fichier (si les scripts sont autorises).

$ErrorActionPreference = "Stop"

# --- Configuration locale (cf. infra/docker-compose.yml) ---
$env:DATABASE_URL = "postgres://computeshare:dev@localhost:5432/computeshare?sslmode=disable"
$env:PORT = "8081"

# --- PostgreSQL doit etre joignable, sinon l'orchestrateur partirait en mode memoire ---
$postgresUp = Test-NetConnection -ComputerName localhost -Port 5432 -InformationLevel Quiet -WarningAction SilentlyContinue
if (-not $postgresUp) {
    Write-Host "PostgreSQL (port 5432) injoignable : demarrage des conteneurs Docker..." -ForegroundColor Yellow
    docker compose -f (Join-Path $PSScriptRoot "..\infra\docker-compose.yml") up -d
    Write-Host "Attente de PostgreSQL..." -ForegroundColor Yellow
    for ($i = 0; $i -lt 15; $i++) {
        Start-Sleep -Seconds 2
        if (Test-NetConnection -ComputerName localhost -Port 5432 -InformationLevel Quiet -WarningAction SilentlyContinue) { break }
    }
}

Set-Location (Join-Path $PSScriptRoot "..\services\orchestrator")
Write-Host ""
Write-Host "Orchestrateur ComputeShare : http://localhost:8081  (Ctrl+C pour arreter)" -ForegroundColor Green
Write-Host "Console client B2B        : http://localhost:8081/console" -ForegroundColor Green
Write-Host ""
go run .
