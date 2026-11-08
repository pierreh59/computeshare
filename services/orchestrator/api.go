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
// Sprint 27 : chaque asset reference doit appartenir a l'org.
func v1Submit(orgs *OrgStore, assets *AssetStore, c *Chunker, b *Billing, accounts *AccountStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Sprint 29 : un compte dont l adresse e-mail n est pas
		// verifiee est limite : aucune soumission de job (le lien de
		// verification est renvoyable depuis la console).
		if accounts != nil {
			if u := accounts.UserByOrg(org.ID); u != nil && !u.EmailVerified {
				http.Error(w, "verifiez votre adresse e-mail avant de soumettre un job : cliquez sur le lien recu par e-mail (renvoyable depuis votre console)", http.StatusForbidden)
				return
			}
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
		// Sprint 27 : un job ne peut referencer que les assets de
		// SON organisation (sinon : lecture des donnees d'autrui).
		for _, id := range job.AssetIDs {
			a := assets.Get(id)
			if a == nil || a.OrgID != org.ID {
				http.Error(w, "asset inconnu ou non possede : "+id, http.StatusBadRequest)
				return
			}
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
		// Sprint 27 : le journal montre les entrees du job.
		if len(job.AssetIDs) > 0 {
			runtimeLabel += fmt.Sprintf(", %d asset(s)", len(job.AssetIDs))
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
		// Sprint 26 : chaque chunk termine indique si son fichier de
		// resultat est telechargeable (URL + taille).
		out := []map[string]interface{}{}
		for _, ch := range c.Results(jobID) {
			row := map[string]interface{}{
				"chunk_id":      ch.ID,
				"node_id":       ch.NodeID,
				"result_sha256": ch.ResultSHA256,
				"duration_secs": ch.DurationSecs,
			}
			if size := resultFileInfo(ch.ID); size > 0 {
				row["download_url"] = "/v1/results/" + ch.ID
				row["size_bytes"] = size
			}
			out = append(out, row)
		}
		writeJSON(w, out)
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
