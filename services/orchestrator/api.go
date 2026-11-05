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
		// LAI-55 : un job_id deja utilise est refuse (idempotence).
		if c.JobExists(job.ID) {
			http.Error(w, "job id already exists", http.StatusConflict)
			return
		}
		chunks := c.Submit(job, org.ID)
		if chunks == nil {
			http.Error(w, "no node available", http.StatusServiceUnavailable)
			return
		}
		dashLog("job_submit", fmt.Sprintf("job %s soumis par %s (%d taches, %s)",
			job.ID, org.Name, len(chunks), job.Command))
		writeJSON(w, map[string]interface{}{
			"job_id":     job.ID,
			"task_count": len(chunks),
			"status":     "queued",
		})
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
			"job_id":      jobID,
			"status":      status,
			"chunks":      st,
			"cost_eur":    costEur,
			"vat_eur":     vatEur,
			"gpu_hours":   gpuHours,
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
