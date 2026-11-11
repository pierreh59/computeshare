package main

// Sprint 38 : tests de la recherche de compte du dashboard admin
// (GET /admin/dash/usersearch). Recherche par e-mail, nom, id, org-id,
// node-id + infos complementaires (solde, jobs, machines, KYC).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAdminUserSearch(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("ADMIN_KEY", "")
	store := NewStore()
	fraud := NewFraud()
	reg := NewRegistry(store, fraud)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(reg, pricing, store, fraud, wallet, billing)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	h := adminDashUserSearch(accounts, billing, chunker, reg, wallet)

	u, _, _, code := accounts.SignUp("client.dupont@example.fr", "MotDePasse38!", "client", "Client Dupont", true, nil)
	if code != http.StatusCreated {
		t.Fatalf("signup client attendu 201, recu %d", code)
	}
	billing.TopUp(u.OrgID, 8.0)
	us, _, _, code := accounts.SignUp("martin@example.fr", "MotDePasse38!", "seller", "Fournisseur Martin", true, nil)
	if code != http.StatusCreated {
		t.Fatalf("signup seller attendu 201, recu %d", code)
	}
	reg.Register("node-s38", "RTX 5070", "prosumer", "FR", "fp-s38", false, false)
	reg.BindSeller("node-s38", us.ID)
	wallet.CreditPayout("node-s38", 0.12, "chunk-s38")

	get := func(q, key string) (*httptest.ResponseRecorder, []map[string]interface{}) {
		req := httptest.NewRequest("GET", "/admin/dash/usersearch?q="+q, nil)
		if key != "" {
			req.Header.Set("X-Admin-Key", key)
		}
		w := httptest.NewRecorder()
		h(w, req)
		var out struct {
			Results []map[string]interface{} `json:"results"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w, out.Results
	}

	// Sans cle : interdit.
	w, _ := get("dupont", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("403 attendu sans cle, recu %d", w.Code)
	}

	// Recherche par nom (insensible a la casse + accent via minuscules).
	w, res := get("dupont", "dev-admin-key")
	if w.Code != http.StatusOK || len(res) != 1 {
		t.Fatalf("1 resultat attendu pour dupont : code=%d n=%d", w.Code, len(res))
	}
	if res[0]["role"] != "client" || res[0]["balance_eur"] != float64(8.0) {
		t.Fatalf("resultat client incoherent : %v", res[0])
	}
	if res[0]["org_id"] != u.OrgID {
		t.Fatalf("org_id attendu %s : %v", u.OrgID, res[0])
	}

	// Recherche par morceau d e-mail.
	_, res = get("martin@example", "dev-admin-key")
	if len(res) != 1 || res[0]["role"] != "seller" {
		t.Fatalf("resultat seller attendu par e-mail : %v", res)
	}

	// Recherche par node-id : retrouve le fournisseur proprietaire.
	_, res = get("node-s38", "dev-admin-key")
	if len(res) != 1 || res[0]["node_hit"] != "node-s38" {
		t.Fatalf("recherche par node-id doit retrouver le fournisseur : %v", res)
	}
	if res[0]["gains_eur"] != 0.12 || res[0]["node_count"] != float64(1) {
		t.Fatalf("infos fournisseur incoherentes : %v", res[0])
	}

	// Recherche par org-id.
	_, res = get(strings.ToLower(u.OrgID), "dev-admin-key")
	if len(res) != 1 || res[0]["id"] != u.ID {
		t.Fatalf("recherche par org-id doit retrouver le client : %v", res)
	}

	// Moins de 2 caracteres : vide, pas d erreur.
	_, res = get("d", "dev-admin-key")
	if len(res) != 0 {
		t.Fatalf("requete trop courte doit renvoyer vide : %v", res)
	}

	// Aucun resultat : tableau vide (jamais null).
	_, res = get("zzzzz", "dev-admin-key")
	if len(res) != 0 {
		t.Fatalf("aucun resultat attendu : %v", res)
	}
}
