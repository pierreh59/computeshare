package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Sprint 23 : OrgJobs liste les jobs d'UNE organisation uniquement,
// avec les comptages par statut, et respecte la limite.
func TestOrgJobsParOrganisation(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, false)

	chunker.Submit(Job{ID: "job-a1", Command: "echo", TaskCount: 2}, "org-a")
	chunker.Submit(Job{ID: "job-a2", Command: "echo", TaskCount: 1}, "org-a")
	chunker.Submit(Job{ID: "job-b1", Command: "echo", TaskCount: 3}, "org-b")

	jobs := chunker.OrgJobs("org-a", 0)
	if len(jobs) != 2 {
		t.Fatalf("org-a doit voir 2 jobs, vu %d", len(jobs))
	}
	if jobs[0]["job_id"] != "job-a1" || jobs[1]["job_id"] != "job-a2" {
		t.Fatalf("ordre attendu job-a1 puis job-a2, vu %v", jobs)
	}
	if jobs[0]["task_count"] != 2 {
		t.Fatalf("job-a1 doit avoir 2 taches, vu %v", jobs[0]["task_count"])
	}
	st := jobs[0]["chunks"].(map[string]int)
	if st["queued"] != 2 {
		t.Fatalf("les 2 taches doivent etre en file, vu %v", st)
	}

	jobsB := chunker.OrgJobs("org-b", 0)
	if len(jobsB) != 1 || jobsB[0]["job_id"] != "job-b1" {
		t.Fatalf("org-b doit voir uniquement job-b1, vu %v", jobsB)
	}

	// La limite garde les plus recents.
	limited := chunker.OrgJobs("org-a", 1)
	if len(limited) != 1 || limited[0]["job_id"] != "job-a2" {
		t.Fatalf("limite 1 doit garder job-a2 (le plus recent), vu %v", limited)
	}
}

// Sprint 23 : GET /v1/jobs renvoie la liste des jobs de l'org avec
// statut global et cout, et ne fuit jamais les jobs d'une autre org.
func TestV1JobsListEndpoint(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, false)

	store := NewStore()
	orgs := NewOrgStore(store)
	billing := NewBilling(store)
	orgA, keyA, _ := orgs.CreateOrg("Org A")
	orgB, keyB, _ := orgs.CreateOrg("Org B")

	chunker.Submit(Job{ID: "job-x1", Command: "echo", TaskCount: 2}, orgA.ID)
	chunker.Submit(Job{ID: "job-x2", Command: "echo", TaskCount: 1}, orgB.ID)

	handler := v1JobsList(orgs, chunker, billing)

	// org A : voit job-x1 uniquement.
	r := httptest.NewRequest("GET", "/v1/jobs", nil)
	r.Header.Set("Authorization", "Bearer "+keyA)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("statut HTTP %d, attendu 200", w.Code)
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("reponse non JSON : %v", err)
	}
	if len(list) != 1 || list[0]["job_id"] != "job-x1" {
		t.Fatalf("org A doit voir uniquement job-x1, vu %v", list)
	}
	if list[0]["status"] != "running" {
		t.Fatalf("statut attendu running (taches en file), vu %v", list[0]["status"])
	}
	if _, ok := list[0]["cost_eur"]; !ok {
		t.Fatal("la reponse doit inclure cost_eur")
	}

	// org B : voit job-x2 uniquement.
	r2 := httptest.NewRequest("GET", "/v1/jobs", nil)
	r2.Header.Set("Authorization", "Bearer "+keyB)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	var list2 []map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &list2)
	if len(list2) != 1 || list2[0]["job_id"] != "job-x2" {
		t.Fatalf("org B doit voir uniquement job-x2, vu %v", list2)
	}

	// Sans cle : 401.
	r3 := httptest.NewRequest("GET", "/v1/jobs", nil)
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, r3)
	if w3.Code != 401 {
		t.Fatalf("sans cle : attendu 401, vu %d", w3.Code)
	}

	// Le POST sur le handler de liste : 405 (le dispatch de main.go
	// route le POST vers la soumission, mais le handler se protege).
	r4 := httptest.NewRequest("POST", "/v1/jobs", nil)
	r4.Header.Set("Authorization", "Bearer "+keyA)
	w4 := httptest.NewRecorder()
	handler.ServeHTTP(w4, r4)
	if w4.Code != 405 {
		t.Fatalf("POST via handler GET : attendu 405, vu %d", w4.Code)
	}
}
