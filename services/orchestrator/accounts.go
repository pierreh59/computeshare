package main

// Sprint 15 (LAI-57) - Comptes utilisateurs : inscription libre et
// connexion pour les deux cotes du marche.
//   - role "client"  : besoin de puissance (interface : entreprise).
//     Le compte est lie a une organisation B2B existante (credits
//     prepayes + cle API v1).
//   - role "seller"  : propose sa puissance (interface : fournisseur).
//     Le compte possede un jeton d'enrolement personnel (X-Node-Token).
// Sprint 15c (LAI-60) - RGPD : consentement horodate, modification du
// profil et du mot de passe, export des donnees (portabilite),
// suppression/anonymisation (droit a l'effacement), revocation de
// toutes les sessions au changement de mot de passe, limitation du
// rythme de connexion (anti force brute).
//
// Mots de passe : bcrypt. Sessions : jeton signe HMAC-SHA256 (secret
// SESSION_SECRET) embarquant une epoque de revocation, expiration 30
// jours, aucun stockage serveur.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User : compte utilisateur (entreprise ou fournisseur).
type User struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	Role           string     `json:"role"` // client | seller (interne)
	DisplayName    string     `json:"display_name"`
	OrgID          string     `json:"org_id,omitempty"`
	ConsentAt      time.Time  `json:"consent_at,omitempty"`  // preuve de consentement RGPD
	SessionsBefore time.Time  `json:"-"`                     // epoque : sessions anterieures revoquees
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`  // compte supprime (anonymise)
	CreatedAt      time.Time  `json:"created_at"`
}

// AccountStore : comptes + index email / jetons fournisseur / sessions.
type AccountStore struct {
	mu           sync.RWMutex
	users        map[string]*User
	byEmail      map[string]string
	passwords    map[string]string // user ID -> hash bcrypt
	sellerTokens map[string]string // SHA-256(jeton) -> user ID
	orgs         *OrgStore
	store        *Store
	secret       []byte
	attempts     map[string]loginAttempt // anti force brute (login)
}

type loginAttempt struct {
	fails    int
	blockedT time.Time
}

func NewAccountStore(store *Store, orgs *OrgStore) *AccountStore {
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		secret = "dev-session-secret"
	}
	return &AccountStore{
		users:        make(map[string]*User),
		byEmail:      make(map[string]string),
		passwords:    make(map[string]string),
		sellerTokens: make(map[string]string),
		orgs:         orgs,
		store:        store,
		secret:       []byte(secret),
		attempts:     make(map[string]loginAttempt),
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

func validEmail(s string) bool {
	at := strings.Index(s, "@")
	return at > 0 && strings.Contains(s[at:], ".") && len(s) <= 254
}

func validPassword(p string) bool {
	return len(p) >= 8 && len(p) <= 200
}

const maxLoginFails = 5
const loginBlockTime = 15 * time.Minute

// SignUp cree un compte. consent = acceptation des CGU + politique de
// confidentialite (RGPD art. 6/7). Retourne l'utilisateur, la cle API
// (entreprise) ou le jeton de noeud personnel (fournisseur), et le
// jeton de session.
func (a *AccountStore) SignUp(email, password, role, name string, consent bool) (*User, string, string, int) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) || !validPassword(password) {
		return nil, "", "", http.StatusBadRequest
	}
	if role != "client" && role != "seller" {
		return nil, "", "", http.StatusBadRequest
	}
	if !consent {
		return nil, "", "", http.StatusForbidden
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, taken := a.byEmail[email]; taken {
		return nil, "", "", http.StatusConflict
	}

	display := strings.TrimSpace(name)
	if display == "" {
		display = strings.Split(email, "@")[0]
	}

	u := &User{
		ID:             "usr-" + randomHex(4),
		Email:          email,
		Role:           role,
		DisplayName:    display,
		ConsentAt:      time.Now(),
		SessionsBefore: time.Unix(0, 0),
		CreatedAt:      time.Now(),
	}
	secret := ""
	if role == "client" {
		org, key, err := a.orgs.CreateOrg(display)
		if err != nil {
			log.Printf("accounts: creation org pour %s: %v", email, err)
			return nil, "", "", http.StatusInternalServerError
		}
		u.OrgID = org.ID
		secret = key
	} else {
		secret = "csnode_" + randomHex(16)
		a.sellerTokens[hashKey(secret)] = u.ID
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", "", http.StatusInternalServerError
	}
	a.users[u.ID] = u
	a.byEmail[email] = u.ID
	a.passwords[u.ID] = string(hash)
	a.store.SaveUser(u, string(hash), hashKey(secret))
	return u, secret, a.newSession(u), http.StatusCreated
}

// Login authentifie par email + mot de passe, avec blocage temporaire
// apres maxLoginFails echecs (anti force brute).
func (a *AccountStore) Login(email, password string) (*User, string, int) {
	email = strings.ToLower(strings.TrimSpace(email))

	a.mu.Lock()
	if att, ok := a.attempts[email]; ok && time.Now().Before(att.blockedT) {
		a.mu.Unlock()
		return nil, "", http.StatusTooManyRequests
	}
	a.mu.Unlock()

	a.mu.RLock()
	id, ok := a.byEmail[email]
	hash := ""
	if ok {
		hash = a.passwords[id]
	}
	user := a.users[id]
	a.mu.RUnlock()
	bad := !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil
	if bad {
		a.mu.Lock()
		att := a.attempts[email]
		att.fails++
		if att.fails >= maxLoginFails {
			att.fails = 0
			att.blockedT = time.Now().Add(loginBlockTime)
			log.Printf("accounts: connexion bloquee pour %s (%d echecs)", email, maxLoginFails)
		}
		a.attempts[email] = att
		a.mu.Unlock()
		return nil, "", http.StatusUnauthorized
	}
	a.mu.Lock()
	delete(a.attempts, email)
	a.mu.Unlock()
	return user, a.newSession(user), http.StatusOK
}

// Session signee : "<uid>.<exp>.<epoque>.<hmac>". L'epoque est la date
// de derniere revocation (SessionsBefore) : changer le mot de passe
// invalide toutes les sessions emises avant.
const sessionTTL = 30 * 24 * time.Hour

func (a *AccountStore) newSession(u *User) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := u.ID + "." + itoa(exp) + "." + itoa(u.SessionsBefore.Unix())
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// Authenticate verifie le jeton Bearer de session et retourne le user.
func (a *AccountStore) Authenticate(r *http.Request) *User {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(h, prefix), ".")
	if len(parts) != 4 {
		return nil
	}
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	if !hmac.Equal([]byte(parts[3]), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return nil
	}
	var exp, epoch int64
	for _, c := range parts[1] + parts[2] {
		if c < '0' || c > '9' {
			return nil
		}
	}
	if _, err := fmtSscan(parts[1], &exp); err != nil {
		return nil
	}
	if _, err := fmtSscan(parts[2], &epoch); err != nil {
		return nil
	}
	if time.Now().Unix() > exp {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	u := a.users[parts[0]]
	if u == nil || u.DeletedAt != nil {
		return nil
	}
	if u.SessionsBefore.Unix() != epoch {
		return nil // session anterieure a une revocation
	}
	return u
}

// SellerIDForToken : un jeton de noeud personnel designe un fournisseur.
func (a *AccountStore) SellerIDForToken(token string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sellerTokens[hashKey(token)]
}

// Restore recharge les comptes depuis la BDD au demarrage.
func (a *AccountStore) Restore(id, email, pwHash, role, display, orgID, tokenHash string,
	consentAt, sessionsBefore time.Time, deletedAt *time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if deletedAt != nil {
		return // compte supprime : rien a recharger
	}
	u := &User{
		ID: id, Email: email, Role: role, DisplayName: display, OrgID: orgID,
		ConsentAt: consentAt, SessionsBefore: sessionsBefore, CreatedAt: consentAt,
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	a.users[id] = u
	a.byEmail[email] = id
	a.passwords[id] = pwHash
	if tokenHash != "" {
		a.sellerTokens[tokenHash] = id
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [21]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// fmtSscan evite d'importer fmt juste pour deux ParseInt.
func fmtSscan(s string, dst *int64) (int, error) {
	n := int64(0)
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadDigit
		}
		n = n*10 + int64(c-'0')
	}
	*dst = n
	return 1, nil
}

type sscanErr string

func (e sscanErr) Error() string { return string(e) }

const errBadDigit = sscanErr("chiffre invalide")

// ---- Handlers HTTP ----

func writeErr(w http.ResponseWriter, code int, msg string) {
	http.Error(w, msg, code)
}

// POST /auth/signup {email, password, role, name, consent}
func (a *AccountStore) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Name     string `json:"name"`
		Consent  bool   `json:"consent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	u, secret, session, code := a.SignUp(req.Email, req.Password, req.Role, req.Name, req.Consent)
	if code == http.StatusConflict {
		writeErr(w, code, "email deja utilise")
		return
	}
	if code == http.StatusForbidden {
		writeErr(w, code, "consentement requis")
		return
	}
	if u == nil {
		writeErr(w, code, "invalid")
		return
	}
	w.WriteHeader(http.StatusCreated)
	resp := map[string]interface{}{"user": u, "session_token": session}
	if u.Role == "client" {
		resp["api_key"] = secret
	} else {
		resp["node_token"] = secret
	}
	writeJSON(w, resp)
}

// POST /auth/login {email, password}
func (a *AccountStore) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	u, session, code := a.Login(req.Email, req.Password)
	if u == nil {
		if code == http.StatusTooManyRequests {
			writeErr(w, code, "trop de tentatives, reessayez dans 15 minutes")
			return
		}
		writeErr(w, code, "identifiants invalides")
		return
	}
	writeJSON(w, map[string]interface{}{"user": u, "session_token": session})
}

// GET /auth/me : profil du compte authentifie (Bearer session).
func (a *AccountStore) handleMe(w http.ResponseWriter, r *http.Request) {
	u := a.Authenticate(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, u)
}

// withSession protege un handler par session valide et passe l'utilisateur.
func withSession(a *AccountStore, h func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.Authenticate(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r, u)
	}
}

// POST /auth/seller-token : ROTATION du jeton de noeud personnel du
// fournisseur authentifie. L'ancien jeton est immediatement invalide.
func (a *AccountStore) handleSellerToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u := a.Authenticate(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if u.Role != "seller" {
		writeErr(w, http.StatusForbidden, "compte fournisseur requis")
		return
	}
	token := "csnode_" + randomHex(16)
	a.mu.Lock()
	for h, id := range a.sellerTokens {
		if id == u.ID {
			delete(a.sellerTokens, h)
		}
	}
	a.sellerTokens[hashKey(token)] = u.ID
	a.store.SaveSellerToken(u.ID, hashKey(token))
	a.mu.Unlock()
	writeJSON(w, map[string]string{"node_token": token})
}

// GET /seller/nodes : les noeuds du fournisseur authentifie (session).
func (a *AccountStore) handleSellerNodes(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.Authenticate(r)
		if u == nil || u.Role != "seller" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeJSON(w, reg.NodesOfUser(u.ID))
	}
}

// GET /seller/wallet : solde agrege des wallets des noeuds du
// fournisseur authentifie + detail par noeud.
func (a *AccountStore) handleSellerWallet(reg *Registry, wallet *Wallet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.Authenticate(r)
		if u == nil || u.Role != "seller" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		nodes := reg.NodesOfUser(u.ID)
		total := 0.0
		detail := []map[string]interface{}{}
		for _, n := range nodes {
			b := wallet.Balance(n.ID)
			total += b
			detail = append(detail, map[string]interface{}{
				"node_id": n.ID, "balance": b,
				"online": n.Online, "gpu_model": n.GPUModel,
			})
		}
		writeJSON(w, map[string]interface{}{
			"total_balance": total, "nodes": detail,
		})
	}
}

// POST /auth/profile {name} : modification du nom affiche.
func (a *AccountStore) handleProfile(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	a.mu.Lock()
	u.DisplayName = strings.TrimSpace(req.Name)
	display := u.DisplayName
	a.mu.Unlock()
	a.store.SaveUserProfile(u.ID, display)
	writeJSON(w, u)
}

// POST /auth/password {current_password, new_password} : changement de
// mot de passe. Revoque toutes les sessions existantes, retourne une
// session fraiche pour l'appareil en cours.
func (a *AccountStore) handlePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	if !validPassword(req.NewPassword) {
		writeErr(w, http.StatusBadRequest, "mot de passe : 8 caracteres minimum")
		return
	}
	a.mu.RLock()
	hash := a.passwords[u.ID]
	a.mu.RUnlock()
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)) != nil {
		writeErr(w, http.StatusUnauthorized, "mot de passe actuel incorrect")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	a.mu.Lock()
	u.SessionsBefore = time.Now()
	a.passwords[u.ID] = string(newHash)
	a.store.SaveUserPassword(u.ID, string(newHash), u.SessionsBefore)
	session := a.newSession(u)
	a.mu.Unlock()
	writeJSON(w, map[string]string{"session_token": session})
}

// GET /auth/export : portabilite RGPD - export JSON des donnees du
// compte (profil + organisation et solde, ou machines et gains).
func (a *AccountStore) handleExport(reg *Registry, wallet *Wallet, billing *Billing) func(http.ResponseWriter, *http.Request, *User) {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		out := map[string]interface{}{
			"exported_at": time.Now().Format(time.RFC3339),
			"account": map[string]interface{}{
				"id": u.ID, "email": u.Email, "display_name": u.DisplayName,
				"role": u.Role, "created_at": u.CreatedAt,
				"consent_at": u.ConsentAt,
			},
		}
		if u.Role == "client" && u.OrgID != "" {
			out["organization"] = map[string]interface{}{
				"org_id":       u.OrgID,
				"balance_eur":  billing.Balance(u.OrgID),
			}
		}
		if u.Role == "seller" {
			nodes := reg.NodesOfUser(u.ID)
			total := 0.0
			list := []map[string]interface{}{}
			for _, n := range nodes {
				b := wallet.Balance(n.ID)
				total += b
				list = append(list, map[string]interface{}{
					"node_id": n.ID, "gpu_model": n.GPUModel,
					"gpu_category": n.GPUCategory, "last_seen": n.LastSeen,
					"balance_eur": b,
				})
			}
			out["machines"] = list
			out["total_gains_eur"] = total
		}
		w.Header().Set("Content-Disposition", "attachment; filename=computeshare-export.json")
		writeJSON(w, out)
	}
}

// DELETE /auth/account {password} : droit a l'effacement RGPD.
// Anonymisation immediate : email, nom, mot de passe et jetons sont
// effaces ; les ecritures comptables anonymisees sont conservees
// (obligation legale de conservation des donnees de facturation).
func (a *AccountStore) handleDeleteAccount(reg *Registry) func(http.ResponseWriter, *http.Request, *User) {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid")
			return
		}
		a.mu.RLock()
		hash := a.passwords[u.ID]
		a.mu.RUnlock()
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
			writeErr(w, http.StatusUnauthorized, "mot de passe incorrect")
			return
		}
		now := time.Now()
		a.mu.Lock()
		delete(a.byEmail, u.Email)
		delete(a.passwords, u.ID)
		for h, id := range a.sellerTokens {
			if id == u.ID {
				delete(a.sellerTokens, h)
			}
		}
		// Detache les machines du compte (elles redeviennent orphelines
		// puis disparaissent faute de heartbeat).
		nodes := reg.NodesOfUser(u.ID)
		u.Email = u.ID + "@supprime.local"
		u.DisplayName = "Compte supprime"
		u.OrgID = ""
		u.DeletedAt = &now
		a.store.SaveUserDeleted(u)
		a.mu.Unlock()
		for _, n := range nodes {
			reg.UnbindSeller(n.ID)
		}
		log.Printf("accounts: compte %s supprime (RGPD), %d machine(s) detachee(s)", u.ID, len(nodes))
		writeJSON(w, map[string]string{"deleted": "ok"})
	}
}
