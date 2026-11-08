package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newTestStack32 : comme newTestStack31 mais retourne aussi le wallet
// (le Reporter lit le grand livre des versements fournisseurs).
func newTestStack32(t *testing.T) (*Registry, *Chunker, *Billing, *Wallet) {
	t.Helper()
	os.Setenv("DATABASE_URL", "")
	store := NewStore()
	fraud := NewFraud()
	reg := NewRegistry(store, fraud)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(reg, pricing, store, fraud, wallet, billing)
	return reg, chunker, billing, wallet
}

// Sprint 32 : un client verifie recharge, soumet un job de 2 taches
// et le complete -> le bilan compte heures, taches, facture HT,
// versements fournisseurs, noeuds actifs et economie vs AWS.
func TestBilanHebdoAgregats(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker, billing, wallet := newTestStack32(t)
	reg.Register("node-r32", "GPU R32", "prosumer", "europe", "fp-r32", false, false)
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	accounts.emails = NewEmailer()
	setNotifier(NewNotifier(accounts, accounts.emails))

	u, key, _, code := accounts.SignUp("client32@computeshare.fr", "MotDePasse32!", "client", "Client 32", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	rec := httptest.NewRecorder()
	accounts.handleVerify(rec, v)
	if rec.Code != 200 {
		t.Fatalf("verification refusee : HTTP %d", rec.Code)
	}
	billing.TopUp(u.OrgID, 10)

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	body := `{"id":"job-s32-a","runtime":"allowlist","command":"echo","task_count":2}`
	req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	sub.ServeHTTP(rec2, req)
	if rec2.Code != 200 {
		t.Fatalf("soumission refusee : HTTP %d %s", rec2.Code, rec2.Body.String())
	}
	for {
		ch := chunker.NextFor("node-r32")
		if ch == nil {
			break
		}
		chunker.Complete(ch.ID, "node-r32", "ok", "sha-32", "detail-32", 1, 0, 1000)
	}

	rp := NewReporter(billing, wallet, reg, accounts.emails)
	s := rp.BuildReport(7)
	f := func(k string) float64 { return asFloat(s[k]) }
	if f("heures_vendues") <= 0 {
		t.Fatalf("heures vendues attendues > 0, obtenu %v", s["heures_vendues"])
	}
	if got := int(f("taches_reussies")); got != 2 {
		t.Fatalf("2 taches reussies attendues, obtenu %d", got)
	}
	if f("facture_ht_eur") <= 0 {
		t.Fatalf("facture HT attendue > 0, obtenu %v", s["facture_ht_eur"])
	}
	if f("verse_fournisseurs_eur") <= 0 {
		t.Fatalf("versements fournisseurs attendus > 0, obtenu %v", s["verse_fournisseurs_eur"])
	}
	if got := int(f("noeuds_actifs")); got != 1 {
		t.Fatalf("1 noeud actif attendu, obtenu %d", got)
	}
	if got := int(f("clients_actifs")); got != 1 {
		t.Fatalf("1 client actif attendu, obtenu %d", got)
	}
	if f("rechargements_eur") != 10 {
		t.Fatalf("rechargements attendus 10 EUR, obtenu %v", s["rechargements_eur"])
	}
	if f("economie_clients_eur") <= 0 {
		t.Fatalf("economie clients attendue > 0 en bêta (tarif < AWS), obtenu %v", s["economie_clients_eur"])
	}
	// Coherence : economie = equivalent AWS - facture HT.
	diff := f("equivalent_aws_eur") - f("facture_ht_eur") - f("economie_clients_eur")
	if diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("economie incoherente : %v", diff)
	}
}

// Sprint 32 : le bouton du dashboard (POST /admin/report/email) exige
// la cle admin, renvoie les stats, et refuse un envoi sans destinataire.
func TestBilanEndpointAdmin(t *testing.T) {
	reg, chunker, billing, wallet := newTestStack32(t)
	_ = chunker
	rp := NewReporter(billing, wallet, reg, NewEmailer())
	h := adminReportEmail(rp)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/admin/report/email", strings.NewReader(`{"email":"ops@computeshare.fr"}`)))
	if rec.Code != 403 {
		t.Fatalf("sans cle admin : HTTP %d attendu, 403", rec.Code)
	}

	os.Setenv("REPORT_EMAIL", "")
	req := httptest.NewRequest("POST", "/admin/report/email", strings.NewReader(`{"email":"ops@computeshare.fr","days":7}`))
	req.Header.Set("X-Admin-Key", "dev-admin-key")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != 200 {
		t.Fatalf("avec cle admin : HTTP %d attendu 200 : %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "heures_vendues") {
		t.Fatalf("stats attendues dans la reponse : %s", rec2.Body.String())
	}

	req3 := httptest.NewRequest("POST", "/admin/report/email", strings.NewReader(`{}`))
	req3.Header.Set("X-Admin-Key", "dev-admin-key")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != 400 {
		t.Fatalf("sans destinataire : HTTP %d attendu 400", rec3.Code)
	}
}
