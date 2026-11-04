package main

// Sprint 15 (LAI-57) - Comptes utilisateurs : inscription libre et
// connexion pour les deux cotes du marche.
//   - role "client"  : besoin de puissance. Le compte est lie a une
//     organisation B2B existante (credits prepayes + cle API v1).
//   - role "seller"  : propose sa puissance. Le compte possede un
//     jeton d'enrolement personnel (X-Node-Token) : le jeton partage
//     de la beta reste accepte pour ne pas casser les noeuds existants.
// Mots de passe : bcrypt. Sessions : jeton signe HMAC-SHA256 (secret
// SESSION_SECRET), expiration 30 jours, aucune stockage serveur.

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

// User : compte utilisateur (client ou vendeur).
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Role        string    `json:"role"` // client | seller
	DisplayName string    `json:"display_name"`
	OrgID       string    `json:"org_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AccountStore : comptes + index email / jetons vendeurs / sessions.
type AccountStore struct {
	mu           sync.RWMutex
	users        map[string]*User // id -> user
	byEmail      map[string]string
	passwords    map[string]string // user ID -> hash bcrypt
	sellerTokens map[string]string // SHA-256(jeton) -> user ID (sellers)
	orgs         *OrgStore
	store        *Store
	secret       []byte
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

// SignUp cree un compte. Retourne l'utilisateur, la cle API (client)
// ou le jeton de noeud personnel (vendeur), et le jeton de session.
func (a *AccountStore) SignUp(email, password, role, name string) (*User, string, string, int) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !validEmail(email) {
		return nil, "", "", http.StatusBadRequest
	}
	if len(password) < 8 || len(password) > 200 {
		return nil, "", "", http.StatusBadRequest
	}
	if role != "client" && role != "seller" {
		return nil, "", "", http.StatusBadRequest
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
		ID:          "usr-" + randomHex(4),
		Email:       email,
		Role:        role,
		DisplayName: display,
		CreatedAt:   time.Now(),
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
	return u, secret, a.newSession(u.ID), http.StatusCreated
}

// Login authentifie par email + mot de passe.
func (a *AccountStore) Login(email, password string) (*User, string, int) {
	email = strings.ToLower(strings.TrimSpace(email))
	a.mu.RLock()
	id, ok := a.byEmail[email]
	hash := ""
	if ok {
		hash = a.passwords[id]
	}
	user := a.users[id]
	a.mu.RUnlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, "", http.StatusUnauthorized
	}
	return user, a.newSession(user.ID), http.StatusOK
}

// Session signee : "<uid>.<exp>.<hmac>". Sans stockage serveur.
const sessionTTL = 30 * 24 * time.Hour

func (a *AccountStore) newSession(userID string) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := userID + "." + itoa(exp)
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
	if len(parts) != 3 {
		return nil
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(payload))
	if !hmac.Equal([]byte(parts[2]), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return nil
	}
	var exp int64
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return nil
		}
		exp = exp*10 + int64(c-'0')
	}
	if time.Now().Unix() > exp {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.users[parts[0]]
}

// SellerIDForToken : un jeton de noeud personnel designe un vendeur.
// Retourne "" si inconnu (le jeton partage est traite par nodeauth).
func (a *AccountStore) SellerIDForToken(token string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sellerTokens[hashKey(token)]
}

// Restore recharge les comptes depuis la BDD au demarrage.
func (a *AccountStore) Restore(id, email, pwHash, role, display, orgID, tokenHash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	u := &User{ID: id, Email: email, Role: role, DisplayName: display, OrgID: orgID}
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
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ---- Handlers HTTP ----

func writeErr(w http.ResponseWriter, code int, msg string) {
	http.Error(w, msg, code)
}

// POST /auth/signup {email, password, role, name}
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	u, secret, session, code := a.SignUp(req.Email, req.Password, req.Role, req.Name)
	if code == http.StatusConflict {
		writeErr(w, code, "email deja utilise")
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
		writeErr(w, code, "identifiants invalides")
		return
	}
	resp := map[string]interface{}{"user": u, "session_token": session}
	writeJSON(w, resp)
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
// vendeur authentifie. L'ancien jeton est immediatement invalide (un
// jeton perdu ou compromis se remplace sans intervention admin).
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
		writeErr(w, http.StatusForbidden, "compte vendeur requis")
		return
	}
	token := "csnode_" + randomHex(16)
	a.mu.Lock()
	// Invalide l'ancien jeton : parcourir l'index inverse.
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
