package main

// Sprint 15 (LAI-57) + 15c (LAI-60) - tests des comptes : inscription,
// connexion, sessions, jetons de noeud, rotation + RGPD : consentement,
// modification de profil, mot de passe (revocation des sessions),
// export, suppression/anonymisation, throttle des tentatives.

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
		`{"email":"Client@Exemple.fr","password":"motdepasse123","role":"client","name":"ACME","consent":true}`)
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
		`{"email":"vendeur@exemple.fr","password":"motdepasse123","role":"seller","consent":true}`)
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
	postJSON(t, a.handleSignup, `{"email":"x@y.fr","password":"motdepasse123","role":"client","consent":true}`)
	rec := postJSON(t, a.handleSignup, `{"email":"x@y.fr","password":"motdepasse456","role":"client","consent":true}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("email en double: attendu 409, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"pasunemail","password":"motdepasse123","role":"client","consent":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("email invalide: attendu 400, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"ok@y.fr","password":"court","role":"client","consent":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("mot de passe trop court: attendu 400, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup, `{"email":"ok2@y.fr","password":"motdepasse123","role":"alien","consent":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("role inconnu: attendu 400, eu %d", rec.Code)
	}
}

func TestLoginEtSession(t *testing.T) {
	a, _ := newTestAccounts(t)
	postJSON(t, a.handleSignup, `{"email":"me@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
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
	rec := postJSON(t, a.handleSignup, `{"email":"v@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
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
	postJSON(t, a.handleSignup, `{"email":"c@y.fr","password":"motdepasse123","role":"client","consent":true}`)
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


// Sprint 15c : sans consentement explicite, l'inscription est refusee.
func TestSignupExigeConsentement(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"nc@y.fr","password":"motdepasse123","role":"client"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("sans consentement: attendu 403, eu %d", rec.Code)
	}
	rec = postJSON(t, a.handleSignup,
		`{"email":"nc@y.fr","password":"motdepasse123","role":"client","consent":true}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("avec consentement: attendu 201, eu %d", rec.Code)
	}
}

// Sprint 15c : le consentement est horodate (preuve RGPD).
func TestConsentementHorodate(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"date@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	u := resp["user"].(map[string]interface{})
	if u["consent_at"] == nil || u["consent_at"] == "" {
		t.Error("consent_at doit etre present dans le profil cree")
	}
}

// Sprint 15c : modification du nom affiche (POST /auth/profile).
func TestProfilModifiable(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"p@y.fr","password":"motdepasse123","role":"client","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	sess := resp["session_token"].(string)
	req := httptest.NewRequest("POST", "/auth/profile", strings.NewReader(`{"name":"Nouveau Nom"}`))
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	withSession(a, a.handleProfile)(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("profil: code %d", rec2.Code)
	}
	var u map[string]interface{}
	json.Unmarshal(rec2.Body.Bytes(), &u)
	if u["display_name"] != "Nouveau Nom" {
		t.Errorf("nom non modifie: %v", u["display_name"])
	}
	// Sans session : refuse.
	req2 := httptest.NewRequest("POST", "/auth/profile", strings.NewReader(`{"name":"x"}`))
	rec3 := httptest.NewRecorder()
	withSession(a, a.handleProfile)(rec3, req2)
	if rec3.Code != http.StatusUnauthorized {
		t.Error("profil sans session doit etre refuse")
	}
}

// Sprint 15c : changer de mot de passe revoque les sessions existantes
// et retourne une session fraiche.
func TestChangementMotDePasseRevoqueSessions(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"pw@y.fr","password":"motdepasse123","role":"client","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	oldSess := resp["session_token"].(string)

	// Une seconde session (autre appareil).
	recL := postJSON(t, a.handleLogin, `{"email":"pw@y.fr","password":"motdepasse123"}`)
	var respL map[string]interface{}
	json.Unmarshal(recL.Body.Bytes(), &respL)
	otherSess := respL["session_token"].(string)

	body := `{"current_password":"motdepasse123","new_password":"nouveaupasse456"}`
	req := httptest.NewRequest("POST", "/auth/password", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+oldSess)
	rec2 := httptest.NewRecorder()
	withSession(a, a.handlePassword)(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("changement mdp: code %d, %s", rec2.Code, rec2.Body)
	}
	var resp2 map[string]string
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	fresh := resp2["session_token"]
	if fresh == "" {
		t.Fatal("une session fraiche doit etre retournee")
	}
	// L'ancienne session de l'autre appareil est revoquee.
	for i, s := range []string{oldSess, otherSess} {
		reqM := httptest.NewRequest("GET", "/auth/me", nil)
		reqM.Header.Set("Authorization", "Bearer "+s)
		recM := httptest.NewRecorder()
		a.handleMe(recM, reqM)
		if recM.Code != http.StatusUnauthorized {
			t.Errorf("session %d doit etre revoquee apres changement de mdp", i)
		}
	}
	// La session fraiche fonctionne ; le nouveau mot de passe login.
	reqF := httptest.NewRequest("GET", "/auth/me", nil)
	reqF.Header.Set("Authorization", "Bearer "+fresh)
	recF := httptest.NewRecorder()
	a.handleMe(recF, reqF)
	if recF.Code != http.StatusOK {
		t.Error("la session fraiche doit etre valide")
	}
	recN := postJSON(t, a.handleLogin, `{"email":"pw@y.fr","password":"nouveaupasse456"}`)
	if recN.Code != http.StatusOK {
		t.Errorf("login avec nouveau mdp: attendu 200, eu %d", recN.Code)
	}
	// Mauvais mot de passe actuel : refuse.
	reqB := httptest.NewRequest("POST", "/auth/password",
		strings.NewReader(`{"current_password":"FAUX","new_password":"encoreun789"}`))
	reqB.Header.Set("Authorization", "Bearer "+fresh)
	recB := httptest.NewRecorder()
	withSession(a, a.handlePassword)(recB, reqB)
	if recB.Code != http.StatusUnauthorized {
		t.Error("mauvais mdp actuel doit etre refuse")
	}
}

// Sprint 15c : export des donnees (portabilite RGPD).
func TestExportDonnees(t *testing.T) {
	a, _ := newTestAccounts(t)
	store := NewStore()
	reg := NewRegistry(store, nil)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	rec := postJSON(t, a.handleSignup,
		`{"email":"ex@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	sess := resp["session_token"].(string)
	req := httptest.NewRequest("GET", "/auth/export", nil)
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	withSession(a, a.handleExport(reg, wallet, billing))(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("export: code %d", rec2.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	acc := out["account"].(map[string]interface{})
	if acc["email"] != "ex@y.fr" {
		t.Errorf("export doit contenir l'email, eu %v", acc["email"])
	}
	if out["machines"] == nil {
		t.Error("export fournisseur doit contenir la liste machines")
	}
}

// Sprint 15c : suppression du compte - anonymisation, detachement des
// machines, sessions et jetons revoques.
func TestSuppressionCompteRGPD(t *testing.T) {
	a, _ := newTestAccounts(t)
	store := NewStore()
	reg := NewRegistry(store, nil)
	rec := postJSON(t, a.handleSignup,
		`{"email":"del@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	sess := resp["session_token"].(string)
	nodeTok := resp["node_token"].(string)
	uid := resp["user"].(map[string]interface{})["id"].(string)

	// Machine liee au compte.
	reg.Register("node-del1", "Test GPU", "mid", "eu", "")
	reg.BindSeller("node-del1", uid)
	if len(reg.NodesOfUser(uid)) != 1 {
		t.Fatal("machine liee attendue")
	}

	req := httptest.NewRequest("DELETE", "/auth/account",
		strings.NewReader(`{"password":"motdepasse123"}`))
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	withSession(a, a.handleDeleteAccount(reg))(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("suppression: code %d, %s", rec2.Code, rec2.Body)
	}
	// La session est morte.
	reqM := httptest.NewRequest("GET", "/auth/me", nil)
	reqM.Header.Set("Authorization", "Bearer "+sess)
	recM := httptest.NewRecorder()
	a.handleMe(recM, reqM)
	if recM.Code != http.StatusUnauthorized {
		t.Error("session doit etre revoquee apres suppression")
	}
	// Le jeton de noeud est revoque.
	na := NewNodeAuth(a)
	r1 := httptest.NewRequest("POST", "/", nil)
	r1.Header.Set("X-Node-Token", nodeTok)
	if _, ok := na.sellerID(r1); ok {
		t.Error("jeton de noeud doit etre revoque apres suppression")
	}
	// La machine est detachee.
	if len(reg.NodesOfUser(uid)) != 0 {
		t.Error("la machine doit etre detachee du compte supprime")
	}
	// Re-login impossible.
	recL := postJSON(t, a.handleLogin, `{"email":"del@y.fr","password":"motdepasse123"}`)
	if recL.Code == http.StatusOK {
		t.Error("login sur compte supprime doit echouer")
	}
	// Mauvais mot de passe : suppression refusee.
	rec2b := postJSON(t, a.handleSignup,
		`{"email":"del2@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var respB map[string]interface{}
	json.Unmarshal(rec2b.Body.Bytes(), &respB)
	reqB := httptest.NewRequest("DELETE", "/auth/account",
		strings.NewReader(`{"password":"FAUX"}`))
	reqB.Header.Set("Authorization", "Bearer "+respB["session_token"].(string))
	rec3 := httptest.NewRecorder()
	withSession(a, a.handleDeleteAccount(reg))(rec3, reqB)
	if rec3.Code != http.StatusUnauthorized {
		t.Error("suppression avec mauvais mdp doit etre refusee")
	}
}

// Sprint 15c : throttle - 5 echecs consecutifs bloquent 15 minutes.
func TestThrottleLogin(t *testing.T) {
	a, _ := newTestAccounts(t)
	postJSON(t, a.handleSignup,
		`{"email":"th@y.fr","password":"motdepasse123","role":"client","consent":true}`)
	for i := 0; i < 5; i++ {
		postJSON(t, a.handleLogin, `{"email":"th@y.fr","password":"FAUX"}`)
	}
	rec := postJSON(t, a.handleLogin, `{"email":"th@y.fr","password":"motdepasse123"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("6e tentative: attendu 429, eu %d", rec.Code)
	}
}


// Sprint 15d (LAI-61) : comptes particulier vs societe.

func TestValidSiretLuhn(t *testing.T) {
	if !validSiret("12345678901237") {
		t.Error("SIRET de test a cle Luhn valide refuse")
	}
	if validSiret("12345678901234") {
		t.Error("SIRET a cle Luhn invalide doit etre refuse")
	}
	if validSiret("123456789012") || validSiret("") || validSiret("1234567890123a") {
		t.Error("SIRET doit faire exactement 14 chiffres")
	}
	if !validVat("FR12345678901") || validVat("fr123") || validVat("FR-123") {
		t.Error("validation TVA intracom incorrecte")
	}
}

func TestSignupSocieteEtSiretInvalide(t *testing.T) {
	a, _ := newTestAccounts(t)
	// Inscription societe avec SIRET valide (cle Luhn locale).
	rec := postJSON(t, a.handleSignup,
		`{"email":"soc@y.fr","password":"motdepasse123","role":"client","consent":true,
		  "company":{"name":"ACME SAS","siret":"12345678901237"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup societe: attendu 201, eu %d, %s", rec.Code, rec.Body)
	}
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	u := resp["user"].(map[string]interface{})
	if u["account_type"] != "societe" {
		t.Errorf("account_type attendu societe, eu %v", u["account_type"])
	}
	if u["siret"] != "12345678901237" || u["company_name"] != "ACME SAS" {
		t.Errorf("siret/raison sociale non enregistres: %v / %v", u["siret"], u["company_name"])
	}
	// SIRET a cle invalide : refuse.
	rec2 := postJSON(t, a.handleSignup,
		`{"email":"soc2@y.fr","password":"motdepasse123","role":"client","consent":true,
		  "company":{"siret":"12345678901234"}}`)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("SIRET invalide: attendu 400, eu %d", rec2.Code)
	}
	// Sans societe : compte particulier.
	rec3 := postJSON(t, a.handleSignup,
		`{"email":"part@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var resp3 map[string]interface{}
	json.Unmarshal(rec3.Body.Bytes(), &resp3)
	u3 := resp3["user"].(map[string]interface{})
	if u3["account_type"] != "particulier" {
		t.Errorf("sans societe: attendu particulier, eu %v", u3["account_type"])
	}
}

func TestSetCompanyApresInscription(t *testing.T) {
	a, _ := newTestAccounts(t)
	rec := postJSON(t, a.handleSignup,
		`{"email":"evol@y.fr","password":"motdepasse123","role":"seller","consent":true}`)
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	sess := resp["session_token"].(string)
	// Passage particulier -> societe.
	body := `{"company":{"name":"Ma Societe","siret":"98765432109875","vat_number":"FR12345678901"}}`
	req := httptest.NewRequest("POST", "/auth/company", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sess)
	rec2 := httptest.NewRecorder()
	withSession(a, a.handleCompany)(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("set company: code %d, %s", rec2.Code, rec2.Body)
	}
	var u map[string]interface{}
	json.Unmarshal(rec2.Body.Bytes(), &u)
	if u["account_type"] != "societe" || u["siret_verified"] != false {
		t.Errorf("compte societe attendu (non verifie), eu %v / %v", u["account_type"], u["siret_verified"])
	}
	// SIRET invalide refuse.
	reqB := httptest.NewRequest("POST", "/auth/company",
		strings.NewReader(`{"company":{"siret":"11111111111111"}}`))
	reqB.Header.Set("Authorization", "Bearer "+sess)
	rec3 := httptest.NewRecorder()
	withSession(a, a.handleCompany)(rec3, reqB)
	if rec3.Code != http.StatusBadRequest {
		t.Errorf("SIRET invalide: attendu 400, eu %d", rec3.Code)
	}
	// Retour au statut particulier (donnees societe effacees).
	reqC := httptest.NewRequest("POST", "/auth/company", strings.NewReader(`{"company":null}`))
	reqC.Header.Set("Authorization", "Bearer "+sess)
	rec4 := httptest.NewRecorder()
	withSession(a, a.handleCompany)(rec4, reqC)
	var u2 map[string]interface{}
	json.Unmarshal(rec4.Body.Bytes(), &u2)
	if u2["account_type"] != "particulier" || u2["siret"] != nil && u2["siret"] != "" {
		t.Errorf("retour particulier attendu, eu %v", u2)
	}
}

func TestPlafondsParticulier(t *testing.T) {
	part := &User{AccountType: "particulier"}
	socVerif := &User{AccountType: "societe", SiretVerified: true}
	socAttente := &User{AccountType: "societe"} // SIRET pas encore verifie
	if !TopupAllowed(part, 50) || TopupAllowed(part, 50.01) {
		t.Error("plafond topup particulier = 50 EUR")
	}
	if !TopupAllowed(socVerif, 100000) {
		t.Error("societe verifiee : pas de plafond topup")
	}
	if TopupAllowed(socAttente, 100) {
		t.Error("societe non verifiee : plafond particulier s'applique")
	}
	if !WithdrawAllowed(part, 100, 500) || WithdrawAllowed(part, 100.01, 0) {
		t.Error("plafond retrait mensuel particulier = 100 EUR")
	}
	if WithdrawAllowed(part, 0, 500.01) {
		t.Error("plafond retrait cumule particulier = 500 EUR")
	}
	if !WithdrawAllowed(socVerif, 100000, 100000) {
		t.Error("societe verifiee : pas de plafond retrait")
	}
}
