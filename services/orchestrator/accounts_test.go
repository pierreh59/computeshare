package main

// Sprint 15 (LAI-57) - tests des comptes : inscription client/vendeur,
// connexion, sessions signees, jetons de noeud personnels, rotation.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newTestAccounts(t *testing.T) (*AccountStore, *NodeAuth) {
	t.Helper()
	os.Setenv("DATABASE_URL", "")
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	return accounts, NewNodeAuth(accounts)
}

func postJSON(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestSignupClientCreeOrgEtCle(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"Client@Exemple.fr","password":"motdepasse123","role":"client","name":"ACME"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup client: code %d, corps %s", rec.Code, rec.Body)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["api_key"] == nil || resp["api_key"].(string) == "" {
		t.Error("la cle API du client doit etre retournee a la creation")
	}
	u := resp["user"].(map[string]interface{})
	if u["org_id"] == "" || u["org_id"] == nil {
		t.Error("le compte client doit etre lie a une organisation")
	}
}

func TestSignupSellerDonneJetonPersonnel(t *testing.T) {
	a, na := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"vendeur@exemple.fr","password":"motdepasse123","role":"seller"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup vendeur: code %d", rec.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	tok, _ := resp["node_token"].(string)
	if !strings.HasPrefix(tok, "csnode_") {
		t.Fatalf("jeton vendeur inattendu: %v", resp["node_token"])
	}
	// Le jeton personnel authentifie le noeud ET identifie le vendeur.
	req := httptest.NewRequest("POST", "/register", nil)
	req.Header.Set("X-Node-Token", tok)
	sellerID, ok := na.sellerID(req)
	if !ok || sellerID == "" {
		t.Error("le jeton personnel doit etre accepte et identifier le vendeur")
	}
	// Le jeton partage (dev) reste accepte, sans compte lie.
	req2 := httptest.NewRequest("POST", "/register", nil)
	req2.Header.Set("X-Node-Token", nodeEnrollmentToken())
	if sid, ok := na.sellerID(req2); !ok || sid != "" {
		t.Error("le jeton partage doit etre accepte sans sellerID")
	}
	// Un jeton inconnu est refuse.
	req3 := httptest.NewRequest("POST", "/register", nil)
	req3.Header.Set("X-Node-Token", "csnode_mauvais")
	if _, ok := na.sellerID(req3); ok {
		t.Error("un jeton inconnu doit etre refuse")
	}
}

func TestSignupRefuseDoublonEtFaibles(t *testing.T) {
	a, _ := newTestAccounts(t)
	postJSON(t, a.handleSignup, `{"email":"x@y.fr","password":"motdepasse123","role":"client"}`)
	rec := postJSON(t, a.handleSignup, `{"email":"x@y.fr","password":"motdepasse456","role":"client"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("email en double: attendu 409, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"pasunemail","password":"motdepasse123","role":"client"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("email invalide: attendu 400, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"ok@y.fr","password":"court","role":"client"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("mot de passe trop court: attendu 400, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"ok2@y.fr","password":"motdepasse123","role":"alien"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("role inconnu: attendu 400, eu %d", rec.Code)
	}
}

func TestLoginEtSession(t *testing.T) {
	a, _ := newTestAccounts(t)
	postJSON(t, a.handleSignup, `{"email":"me@y.fr","password":"motdepasse123","role":"seller"}`)
	rec := postJSON(t, a.handleLogin, `{"email":"me@y.fr","password":"MAUVAIS"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("mauvais mot de passe: attendu 401, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleLogin, `{"email":"me@y.fr","password":"motdepasse123"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: code %d", rec.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	sess, _ := resp["session_token"].(string)
	if sess == "" {
		t.Fatal("session absente")
	}
	// GET /auth/me avec la session.
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	a.handleMe(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("/auth/me: attendu 200, eu %d", rec2.Code)
	}
	// Session corrompue refusee.
	req2 := httptest.NewRequest("GET", "/auth/me", nil)
	req2.Header.Set("Authorization", "Bearer "+sess+"xyz")
	rec3 := httptest.NewRecorder()
	a.handleMe(rec3, req2)
	if rec3.Code != http.StatusUnauthorized {
		t.Errorf("session corrompue: attendu 401, eu %d", rec3.Code)
	}
}

func TestRotationJetonVendeur(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup, `{"email":"v@y.fr","password":"motdepasse123","role":"seller"}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	oldTok := resp["node_token"].(string)
	sess := resp["session_token"].(string)

	// Rotation : le vendeur authentifie demande un nouveau jeton.
	req := httptest.NewRequest("POST", "/auth/seller-token", nil)
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	a.handleSellerToken(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("rotation: code %d", rec2.Code)
	}
	var resp2 map[string]string
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	newTok := resp2["node_token"]
	if newTok == "" || newTok == oldTok {
		t.Fatal("le nouveau jeton doit etre different")
	}
	// L'ancien est invalide, le nouveau est valide.
	na := NewNodeAuth(a)
	r1 := httptest.NewRequest("POST", "/", nil)
	r1.Header.Set("X-Node-Token", oldTok)
	if _, ok := na.sellerID(r1); ok {
		t.Error("l'ancien jeton doit etre refuse apres rotation")
	}
	r2 := httptest.NewRequest("POST", "/", nil)
	r2.Header.Set("X-Node-Token", newTok)
	if _, ok := na.sellerID(r2); !ok {
		t.Error("le nouveau jeton doit etre accepte")
	}
	// Un client ne peut pas demander de jeton de noeud.
	postJSON(t, a.handleSignup, `{"email":"c@y.fr","password":"motdepasse123","role":"client"}`)
	rec3 := postJSON(t, a.handleLogin, `{"email":"c@y.fr","password":"motdepasse123"}`)
	var resp3 map[string]interface{}
	json.Unmarshal(rec3.Body.Bytes(), &resp3)
	req3 := httptest.NewRequest("POST", "/", nil)
	req3.Header.Set("Authorization", "Bearer "+resp3["session_token"].(string))
	rec4 := httptest.NewRecorder()
	a.handleSellerToken(rec4, req3)
	if rec4.Code != http.StatusForbidden {
		t.Errorf("client -> rotation: attendu 403, eu %d", rec4.Code)
	}
}

var _ = bytes.MinRead
