package main

// LAI-30 - API v1 pour les clients B2B (spec : api/openapi.yaml).
// Tous les endpoints exigent "Authorization: Bearer <cle API>".

import (
	"encoding/json"
	"net/http"
	"strings"
)

// POST /v1/jobs : soumet un job, decoupe en chunks par le chunker.
func v1Submit(orgs *OrgStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !orgs.Authenticate(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var job Job
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil || job.ID == "" {
			http.Error(w, "invalid job", http.StatusBadRequest)
			return
		}
		chunks := c.Submit(job)
		if chunks == nil {
			http.Error(w, "no node available", http.StatusServiceUnavailable)
			return
		}
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

// GET /v1/jobs/<id>/status : avancement d'un job.
func v1Status(orgs *OrgStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !orgs.Authenticate(r) {
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
		writeJSON(w, map[string]interface{}{
			"job_id": jobID,
			"status": status,
			"chunks": st,
		})
	}
}

// GET /v1/jobs/<id>/results : chunks termines (checksums inclus).
func v1Results(orgs *OrgStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !orgs.Authenticate(r) {
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
		if !orgs.Authenticate(r) {
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
