package main

// Sprint 37 : tests de l assistant conversationnel (chatbot.go).
// - FAQ : reponse pre-ecrite sur les cas frequents ;
// - mode degrade sans MISTRAL_API_KEY : repli FAQ ;
// - anti-abus : fenetre glissante par IP (anonyme) ;
// - actions : session obligatoire, renvoi d e-mail de verification,
//   historique par e-mail (contenu non technique) ;
// - garde-fou : les reponses FAQ ne contennent jamais de secret.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newTestChatBot(t *testing.T) (*ChatBot, *AccountStore, *Chunker, *Billing, *Wallet, *Registry) {
	t.Helper()
	os.Setenv("DATABASE_URL", "")
	os.Setenv("MISTRAL_API_KEY", "")
	store := NewStore()
	fraud := NewFraud()
	reg := NewRegistry(store, fraud)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(reg, pricing, store, fraud, wallet, billing)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	bot := NewChatBot(accounts, chunker, billing, wallet, reg, nil)
	return bot, accounts, chunker, billing, wallet, reg
}

func postChat(bot *ChatBot, msg string, session string) *httptest.ResponseRecorder {
	body := `{"message":"` + msg + `"}`
	req := httptest.NewRequest("POST", "/chat", strings.NewReader(body))
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
	}
	req.RemoteAddr = "1.2.3.4:10"
	w := httptest.NewRecorder()
	bot.handleChat(w, req)
	return w
}

func TestChatFAQJobBloque(t *testing.T) {
	bot, _, _, _, _, _ := newTestChatBot(t)
	w := postChat(bot, "mon job est bloque, il ne se passe rien", "")
	if w.Code != http.StatusOK {
		t.Fatalf("200 attendu, recu %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["mode"] != "faq" {
		t.Fatalf("mode faq attendu, recu %v", resp["mode"])
	}
	reply, _ := resp["reply"].(string)
	if !strings.Contains(reply, "fichier résultat") {
		t.Fatalf("la reponse doit expliquer le fichier resultat : %s", reply)
	}
}

func TestChatDegradedSansCleAI(t *testing.T) {
	bot, _, _, _, _, _ := newTestChatBot(t)
	w := postChat(bot, "raconte-moi ta vie preferable en crepuscule", "")
	if w.Code != http.StatusOK {
		t.Fatalf("200 attendu, recu %d", w.Code)
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	reply, _ := resp["reply"].(string)
	if !strings.Contains(reply, "questions courantes") {
		t.Fatalf("repli FAQ attendu sans cle IA : %s", reply)
	}
}

func TestChatThrottleAnonyme(t *testing.T) {
	bot, _, _, _, _, _ := newTestChatBot(t)
	for i := 0; i < 20; i++ {
		w := postChat(bot, "question frequente numero", "")
		if w.Code != http.StatusOK {
			t.Fatalf("les 20 premiers messages doivent passer (%d a l essai %d)", w.Code, i)
		}
	}
	w := postChat(bot, "un message de trop", "")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("429 attendu au 21e message anonyme, recu %d", w.Code)
	}
}

func TestChatActionSansSession(t *testing.T) {
	bot, _, _, _, _, _ := newTestChatBot(t)
	req := httptest.NewRequest("POST", "/chat/action", strings.NewReader(`{"action_id":"history_email"}`))
	w := httptest.NewRecorder()
	bot.handleChatAction(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("401 attendu sans session, recu %d", w.Code)
	}
}

func TestChatActionVerificationEtCooldown(t *testing.T) {
	bot, accounts, _, _, _, _ := newTestChatBot(t)
	u, _, _, code := accounts.SignUp("chat37@computeshare.fr", "MotDePasse37!", "client", "Chat 37", true, nil)
	if code != http.StatusCreated {
		t.Fatalf("signup attendu 201, recu %d", code)
	}
	session := accounts.newSession(u)

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/chat/action", strings.NewReader(`{"action_id":"resend_verification"}`))
		req.Header.Set("Authorization", "Bearer "+session)
		w := httptest.NewRecorder()
		bot.handleChatAction(w, req)
		return w
	}
	w := do()
	if w.Code != http.StatusOK {
		t.Fatalf("200 attendu, recu %d : %s", w.Code, w.Body.String())
	}
	// Anti-spam : le 2e renvoi immediate doit repondre ok mais "patientez".
	w2 := do()
	if w2.Code != http.StatusOK {
		t.Fatalf("200 attendu (cooldown), recu %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "patientez") {
		t.Fatalf("le cooldown doit renvoyer le message patientez : %s", w2.Body.String())
	}
}

func TestChatContexteEtHistoriqueClient(t *testing.T) {
	bot, accounts, _, billing, _, _ := newTestChatBot(t)
	u, _, _, code := accounts.SignUp("hist37@computeshare.fr", "MotDePasse37!", "client", "Hist 37", true, nil)
	if code != http.StatusCreated {
		t.Fatalf("signup attendu 201, recu %d", code)
	}
	billing.TopUp(u.OrgID, 12.0)
	ctx := bot.userContext(u)
	if !strings.Contains(ctx, "12.00 EUR") {
		t.Fatalf("le contexte doit contenir le solde : %s", ctx)
	}
	if !strings.Contains(ctx, "NON verifiee") {
		t.Fatalf("le contexte doit signaler le compte non verifie : %s", ctx)
	}
	subject, html := bot.buildHistoryEmail(u)
	if subject == "" || !strings.Contains(html, "Solde de crédits") {
		t.Fatalf("e-mail d historique incoherent : %s / %s", subject, html)
	}
	if !strings.Contains(html, "NON vérifiée") {
		t.Fatalf("l e-mail doit montrer le compte non verifie : %s", html)
	}
}

func TestChatContexteFournisseur(t *testing.T) {
	bot, accounts, _, _, wallet, reg := newTestChatBot(t)
	u, _, _, code := accounts.SignUp("seller37@computeshare.fr", "MotDePasse37!", "seller", "Seller 37", true, nil)
	if code != http.StatusCreated {
		t.Fatalf("signup attendu 201, recu %d", code)
	}
	reg.Register("node-chat37", "RTX 5070", "prosumer", "FR", "fp-chat37", false, false)
	reg.BindSeller("node-chat37", u.ID)
	wallet.CreditPayout("node-chat37", 0.05, "chunk-x")
	ctx := bot.userContext(u)
	if !strings.Contains(ctx, "fournisseur") || !strings.Contains(ctx, "RTX 5070") {
		t.Fatalf("contexte fournisseur incoherent : %s", ctx)
	}
	_, html := bot.buildHistoryEmail(u)
	if !strings.Contains(html, "Gains cumulés") || !strings.Contains(html, "node-chat37") {
		t.Fatalf("e-mail historique fournisseur incoherent : %s", html)
	}
}

func TestChatFAQSansSecretNiTechnique(t *testing.T) {
	interdits := []string{"8080", "POSTGRES", "postgres", "DATABASE", "ADMIN_KEY", "NODE_ENROLLMENT", ".go", "csnode_", "cshare_", "docker-compose"}
	for i := range chatFAQs {
		for _, bad := range interdits {
			if strings.Contains(strings.ToLower(chatFAQs[i].answer), strings.ToLower(bad)) {
				t.Fatalf("la FAQ ne doit jamais mentionner %s : %s", bad, chatFAQs[i].answer)
			}
		}
	}
}
