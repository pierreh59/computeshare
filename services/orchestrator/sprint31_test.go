package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newTestStack31 : pile complete avec le MEME billing que le chunker
// (newTestDash cache le sien : les topups de test partiraient dans
// une autre instance et les soldes seraient faux).
func newTestStack31(t *testing.T) (*Registry, *Chunker, *Billing) {
	t.Helper()
	os.Setenv("DATABASE_URL", "")
	store := NewStore()
	fraud := NewFraud()
	reg := NewRegistry(store, fraud)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(reg, pricing, store, fraud, wallet, billing)
	return reg, chunker, billing
}

// completeAllTasksE2E : un seul noeud reclame et rend toutes les
// taches en file (outil commun des tests du sprint 31).
func completeAllTasksE2E(t *testing.T, c *Chunker, nodeID, outcome string) {
	t.Helper()
	for {
		ch := c.NextFor(nodeID)
		if ch == nil {
			return
		}
		c.Complete(ch.ID, nodeID, outcome, "sha-31", "detail-31", 1, 0, 1000)
	}
}

// Sprint 31 : le dernier chunk rendu declenche UN e-mail "job
// termine" au proprietaire verifie de l org (et un seul).
func TestNotificationJobTermine(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker, billing := newTestStack31(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, true) // sprint 39 : jobs tous GPU
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	accounts.emails = NewEmailer()
	setNotifier(NewNotifier(accounts, accounts.emails))

	u, key, _, code := accounts.SignUp("client31@computeshare.fr", "MotDePasse31!", "client", "Client 31", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}
	// Verification de l adresse (obligatoire pour soumettre).
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	rec := httptest.NewRecorder()
	accounts.handleVerify(rec, v)
	if rec.Code != 200 {
		t.Fatalf("verification refusee : HTTP %d", rec.Code)
	}
	billing.TopUp(u.OrgID, 10) // au-dessus du seuil credits bas

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	body := `{"id":"job-s31-a","runtime":"allowlist","command":"echo","task_count":2}`
	req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	sub.ServeHTTP(rec2, req)
	if rec2.Code != 200 {
		t.Fatalf("soumission refusee : HTTP %d %s", rec2.Code, rec2.Body.String())
	}

	completeAllTasksE2E(t, chunker, "node-a", "ok")

	var jobDone int
	for _, d := range notifier.Deliveries() {
		if d.Kind == "job_done" {
			jobDone++
			if d.Email != u.Email {
				t.Fatalf("e-mail job_done envoye a %s au lieu de %s", d.Email, u.Email)
			}
			if !strings.Contains(d.Ref, "job-s31-a") {
				t.Fatalf("e-mail job_done avec mauvaise reference : %s", d.Ref)
			}
		}
		if d.Kind == "low_credits" {
			t.Fatalf("aucune alerte credits bas attendue avec 10 EUR de solde")
		}
	}
	if jobDone != 1 {
		t.Fatalf("exactement 1 e-mail job_done attendu, %d recus", jobDone)
	}
}

// Sprint 31 : l alerte "credits bas" part UNE seule fois, se rearme
// apres un rechargement, et ne revient pas tant que le solde reste
// sous le seuil.
func TestNotificationCreditsBasUneSeuleFois(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker, billing := newTestStack31(t)
	reg.Register("node-b", "GPU B", "prosumer", "europe", "fp-b", false, true) // sprint 39 : jobs tous GPU
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	accounts.emails = NewEmailer()
	n := NewNotifier(accounts, accounts.emails)
	setNotifier(n)

	u, key, _, _ := accounts.SignUp("client31b@computeshare.fr", "MotDePasse31!", "client", "Client 31b", true, nil)
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	accounts.handleVerify(httptest.NewRecorder(), v)
	billing.TopUp(u.OrgID, 5) // facture mini -> solde juste sous 5

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	soumettre := func(id string) {
		body := `{"id":"` + id + `","runtime":"allowlist","command":"echo","task_count":1}`
		req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		sub.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("soumission %s refusee : HTTP %d", id, rec.Code)
		}
		completeAllTasksE2E(t, chunker, "node-b", "ok")
	}

	compter := func() int {
		c := 0
		for _, d := range n.Deliveries() {
			if d.Kind == "low_credits" {
				c++
			}
		}
		return c
	}

	soumettre("job-s31-b1")
	if compter() != 1 {
		t.Fatalf("1 alerte credits bas attendue apres le passage sous 5 EUR, %d", compter())
	}
	// Toujours sous le seuil : pas de rappel.
	soumettre("job-s31-b2")
	if compter() != 1 {
		t.Fatalf("l alerte credits bas ne doit partir qu une fois, %d", compter())
	}
	// Rechargement admin : le flag se rearme (comme dans main.go),
	// mais le solde est remonte : pas de nouvelle alerte.
	billing.TopUp(u.OrgID, 10)
	n.ResetLowCredits(u.OrgID)
	soumettre("job-s31-b3")
	if compter() != 1 {
		t.Fatalf("solde remonte : pas de nouvelle alerte attendue, %d", compter())
	}
}

// Sprint 31 : opt-out - les preferences a false suppriment TOUTES
// les notifications.
func TestOptOutNotifications(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker, billing := newTestStack31(t)
	reg.Register("node-c", "GPU C", "prosumer", "europe", "fp-c", false, true) // sprint 39 : jobs tous GPU
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	accounts.emails = NewEmailer()
	n := NewNotifier(accounts, accounts.emails)
	setNotifier(n)

	u, key, _, _ := accounts.SignUp("client31c@computeshare.fr", "MotDePasse31!", "client", "Client 31c", true, nil)
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	accounts.handleVerify(httptest.NewRecorder(), v)
	billing.TopUp(u.OrgID, 5)

	// Desactivation via l endpoint console.
	req := httptest.NewRequest("POST", "/auth/notifications",
		bytes.NewReader([]byte(`{"notify_job_done":false,"notify_low_credits":false}`)))
	req.Header.Set("Authorization", "Bearer "+accounts.newSession(u))
	rec := httptest.NewRecorder()
	accounts.handleNotifications(rec, req, u)
	if rec.Code != 200 {
		t.Fatalf("enregistrement des preferences refuse : HTTP %d", rec.Code)
	}
	if u.NotifyJobDone || u.NotifyLowCredits {
		t.Fatalf("les preferences doivent etre desactivees")
	}

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	body := `{"id":"job-s31-c","runtime":"allowlist","command":"echo","task_count":1}`
	req2 := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req2.Header.Set("Authorization", "Bearer "+key)
	sub.ServeHTTP(httptest.NewRecorder(), req2)
	completeAllTasksE2E(t, chunker, "node-c", "ok")

	if len(n.Deliveries()) != 0 {
		t.Fatalf("aucune notification attendue apres opt-out, %d recues", len(n.Deliveries()))
	}
}

// Sprint 31 : un job annule par le client (aucune tache aboutie ni
// echouee) ne declenche PAS d e-mail.
func TestJobAnnuleSansEmail(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	n := NewNotifier(accounts, NewEmailer())
	setNotifier(n)

	// Job entierement annule : JobFinished ne fait rien.
	n.JobFinished("org-x", "job-s31-d", 0, 0, 3, 0, nil)
	if len(n.Deliveries()) != 0 {
		t.Fatalf("un job annule ne doit pas declencher d e-mail, %d recues", len(n.Deliveries()))
	}
}
