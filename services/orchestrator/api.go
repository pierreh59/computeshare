package main

// LAI-30 - API v1 pour les clients B2B (spec : api/openapi.yaml).
// Tous les endpoints exigent "Authorization: Bearer <cle API>".

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// POST /v1/jobs : soumet un job, decoupe en chunks par le chunker.
// LAI-32 : refused si l'organisation n'a plus de credits prepayes.
func v1Submit(orgs *OrgStore, c *Chunker, b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !b.HasCredits(org.ID) {
			http.Error(w, "insufficient credits", http.StatusPaymentRequired)
			return
		}
		var job Job
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil || job.ID == "" {
			http.Error(w, "invalid job", http.StatusBadRequest)
			return
		}
		// Sprint 18 (v2) : validation du runtime conteneur.
		if job.Runtime == "" {
			job.Runtime = "allowlist"
		}
		if job.Runtime != "allowlist" && job.Runtime != "container" {
			http.Error(w, "runtime invalide (allowlist ou container)", http.StatusBadRequest)
			return
		}
		if job.Runtime == "container" {
			// Image = adresse de registre Docker. On refuse les
			// espaces et on borne la longueur (pas d'injection).
			if len(job.Image) == 0 || len(job.Image) > 500 ||
				strings.ContainsAny(job.Image, " \t\"'`;$|&<>") {
				http.Error(w, "image de conteneur invalide", http.StatusBadRequest)
				return
			}
			if job.Command == "" {
				http.Error(w, "commande manquante pour le conteneur", http.StatusBadRequest)
				return
			}
		}
		// LAI-55 : un job_id deja utilise est refuse (idempotence).
		if c.JobExists(job.ID) {
			http.Error(w, "job id already exists", http.StatusConflict)
			return
		}
		chunks := c.Submit(job, org.ID)
		if chunks == nil {
			if job.Gpu {
				http.Error(w, "no gpu-capable node available", http.StatusServiceUnavailable)
			} else if job.Runtime == "container" {
				http.Error(w, "no container-capable node available", http.StatusServiceUnavailable)
			} else {
				http.Error(w, "no node available", http.StatusServiceUnavailable)
			}
			return
		}
		runtimeLabel := job.Runtime
		if runtimeLabel == "container" {
			runtimeLabel = "conteneur " + job.Image
		}
		if job.Gpu {
			runtimeLabel += " [GPU]"
		}
		dashLog("job_submit", fmt.Sprintf("job %s soumis par %s (%d taches, %s, %s)",
			job.ID, org.Name, len(chunks), runtimeLabel, job.Command))
		writeJSON(w, map[string]interface{}{
			"job_id":     job.ID,
			"task_count": len(chunks),
			"status":     "queued",
		})
	}
}

// GET /v1/jobs : liste des jobs de l'organisation (sprint 23) :
// pour chaque job, statuts des taches, statut global et cout reel.
func v1JobsList(orgs *OrgStore, c *Chunker, b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		jobs := c.OrgJobs(org.ID, 50)
		for _, j := range jobs {
			id := j["job_id"].(string)
			chunks := j["chunks"].(map[string]int)
			total := j["task_count"].(int)
			finished := chunks["done"] + chunks["failed"] + chunks["canceled"]
			status := "running"
			if total > 0 && finished == total {
				status = "completed"
			}
			j["status"] = status
			costEur, vatEur, gpuHours := b.JobCost(org.ID, id)
			j["cost_eur"] = costEur
			j["vat_eur"] = vatEur
			j["gpu_hours"] = gpuHours
		}
		writeJSON(w, jobs)
	}
}

// jobIDFrom extrait l'ID d'un chemin /v1/jobs/<id>/<action>.
func jobIDFrom(path, action string) string {
	rest := strings.TrimPrefix(path, "/v1/jobs/")
	rest = strings.TrimSuffix(rest, "/"+action)
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// GET /v1/jobs/<id>/status : avancement d'un job, cout reel compris
// (LAI-31 : la console affiche le cout et l'economie vs AWS).
func v1Status(orgs *OrgStore, c *Chunker, b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		jobID := jobIDFrom(r.URL.Path, "status")
		if jobID == "" {
			http.NotFound(w, r)
			return
		}
		st := c.JobStatus(jobID)
		total := 0
		for _, v := range st {
			total += v
		}
		finished := st["done"] + st["failed"] + st["canceled"]
		status := "running"
		if total > 0 && finished == total {
			status = "completed"
		}
		costEur, vatEur, gpuHours := b.JobCost(org.ID, jobID)
		writeJSON(w, map[string]interface{}{
			"job_id":        jobID,
			"status":        status,
			"chunks":        st,
			"cost_eur":      costEur,
			"vat_eur":       vatEur,
			"gpu_hours":     gpuHours,
			"aws_equiv_eur": gpuHours * awsCompareEurPerHour,
		})
	}
}

// GET /v1/jobs/<id>/results : chunks termines (checksums inclus).
func v1Results(orgs *OrgStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if orgs.Authenticate(r) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		jobID := jobIDFrom(r.URL.Path, "results")
		if jobID == "" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, c.Results(jobID))
	}
}

// POST /v1/jobs/<id>/cancel : annule les chunks pas encore demarres.
func v1Cancel(orgs *OrgStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if orgs.Authenticate(r) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		jobID := jobIDFrom(r.URL.Path, "cancel")
		if jobID == "" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]interface{}{
			"job_id":   jobID,
			"canceled": c.CancelJob(jobID),
		})
	}
}
