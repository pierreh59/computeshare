package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Sprint 29 : un compte dont l adresse e-mail n est pas verifiee ne
// peut pas soumettre de job ; apres le clic sur le lien, il peut.
func TestCompteNonVerifieBloqueJob(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, true) // sprint 39 : jobs tous GPU
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	billing := NewBilling(store)

	u, key, _, code := accounts.SignUp("client29@computeshare.fr", "MotDePasse29!", "client", "Client 29", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}
	billing.TopUp(u.OrgID, 5)

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	body := `{"id":"job-s29-a","runtime":"allowlist","command":"echo","task_count":1}`

	// Non verifie : refuse (403).
	req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	sub.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("compte non verifie doit etre refuse : HTTP %d %s", rec.Code, rec.Body.String())
	}

	// Verification via le lien (simule le clic dans l e-mail).
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	recV := httptest.NewRecorder()
	accounts.handleVerify(recV, v)
	if recV.Code != 200 || !strings.Contains(recV.Body.String(), "verifiee") {
		t.Fatalf("page de verification inattendue : HTTP %d %s", recV.Code, recV.Body.String())
	}
	if !u.EmailVerified {
		t.Fatalf("le compte doit etre verifie apres le clic")
	}

	// Verifie : accepte (200).
	req2 := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req2.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	sub.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("compte verifie doit etre accepte : HTTP %d %s", rec2.Code, rec2.Body.String())
	}
}

// Sprint 29 : le jeton est a usage unique et ne sert que pour son type.
func TestJetonUsageUnique(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	u, _, _, code := accounts.SignUp("unique29@computeshare.fr", "MotDePasse29!", "seller", "Unique 29", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}

	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	if accounts.consumeAuthToken(raw, "reset_password") != nil {
		t.Fatalf("un jeton de verification ne doit pas marcher comme jeton de reset")
	}
	if accounts.consumeAuthToken(raw, "verify_email") == nil {
		t.Fatalf("consommation legitime doit reussir")
	}
	if accounts.consumeAuthToken(raw, "verify_email") != nil {
		t.Fatalf("un jeton consomme ne doit plus marcher (usage unique)")
	}
}

// Sprint 29 : un jeton expire ne marche pas.
func TestJetonExpire(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	u, _, _, code := accounts.SignUp("expire29@computeshare.fr", "MotDePasse29!", "seller", "Expire 29", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}
	raw := accounts.newAuthToken(u.ID, "reset_password", u.Email)
	hash := hashKey(raw)
	accounts.mu.Lock()
	tok := accounts.authTokens[hash]
	tok.expiresAt = time.Now().Add(-time.Minute)
	accounts.mu.Unlock()
	if accounts.consumeAuthToken(raw, "reset_password") != nil {
		t.Fatalf("un jeton expire doit etre refuse")
	}
}

// Sprint 29 : reinitialisation de mot de passe E2E - le lien recu par
// e-mail permet de changer le mot de passe, l ancien ne marche plus,
// et les sessions anterieures sont revoquees.
func TestResetPassword(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	u, _, session, code := accounts.SignUp("reset29@computeshare.fr", "AncienMDP29!", "seller", "Reset 29", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}

	// Session valide avant le reset.
	reqMe := httptest.NewRequest("GET", "/auth/me", nil)
	reqMe.Header.Set("Authorization", "Bearer "+session)
	if accounts.Authenticate(reqMe) == nil {
		t.Fatalf("la session doit etre valide avant le reset")
	}

	raw := accounts.newAuthToken(u.ID, "reset_password", u.Email)
	// Mot de passe trop court : refuse.
	rec0 := httptest.NewRecorder()
	accounts.handleResetPassword(rec0, httptest.NewRequest("POST", "/auth/reset_password",
		strings.NewReader(`{"token":"`+raw+`","new_password":"court"}`)))
	if rec0.Code != 400 {
		t.Fatalf("mot de passe trop court doit etre refuse : HTTP %d", rec0.Code)
	}

	// Reset legitime.
	rec := httptest.NewRecorder()
	accounts.handleResetPassword(rec, httptest.NewRequest("POST", "/auth/reset_password",
		strings.NewReader(`{"token":"`+raw+`","new_password":"NouveauMDP29!"}`)))
	if rec.Code != 200 {
		t.Fatalf("reset refuse : HTTP %d %s", rec.Code, rec.Body.String())
	}

	// L ancien mot de passe ne marche plus, le nouveau oui.
	if _, _, c := accounts.Login("reset29@computeshare.fr", "AncienMDP29!"); c == 200 {
		t.Fatalf("l ancien mot de passe doit etre refuse")
	}
	u2, _, c2 := accounts.Login("reset29@computeshare.fr", "NouveauMDP29!")
	if u2 == nil || c2 != 200 {
		t.Fatalf("le nouveau mot de passe doit marcher : HTTP %d", c2)
	}

	// La session anterieure est revoquee.
	reqMe2 := httptest.NewRequest("GET", "/auth/me", nil)
	reqMe2.Header.Set("Authorization", "Bearer "+session)
	if accounts.Authenticate(reqMe2) != nil {
		t.Fatalf("la session anterieure doit etre revoquee apres un reset")
	}

	// Le jeton est consomme : un deuxieme reset avec le meme lien echoue.
	rec2 := httptest.NewRecorder()
	accounts.handleResetPassword(rec2, httptest.NewRequest("POST", "/auth/reset_password",
		strings.NewReader(`{"token":"`+raw+`","new_password":"AutreMDP29!"}`)))
	if rec2.Code != 400 {
		t.Fatalf("un lien de reset consomme doit echouer : HTTP %d", rec2.Code)
	}
}

// Sprint 29 : mot de passe oublie - reponse identique que le compte
// existe ou non (pas de divulgation), et le lien est envoye si le
// compte existe.
func TestForgotPasswordNeDivulgeRien(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	_, _, _, code := accounts.SignUp("connu29@computeshare.fr", "MotDePasse29!", "client", "Connu 29", true, nil)
	if code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}

	for _, email := range []string{"connu29@computeshare.fr", "inconnu29@computeshare.fr"} {
		rec := httptest.NewRecorder()
		accounts.handleForgotPassword(rec, httptest.NewRequest("POST", "/auth/forgot_password",
			strings.NewReader(`{"email":"`+email+`"}`)))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sent":true`) {
			t.Fatalf("reponse inattendue pour %s : HTTP %d %s", email, rec.Code, rec.Body.String())
		}
	}
}
