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
	"fmt"
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

	// Sprint 29 : adresse e-mail verifiee (clic sur le lien recu par
	// e-mail). Un compte non verifie est limite : pas de soumission
	// de job.
	EmailVerified bool `json:"email_verified"`

	// Sprint 15d : particulier vs societe (LAI-61). Les champs societe
	// sont vides pour un particulier.
	AccountType    string     `json:"account_type"`           // particulier | societe
	CompanyName    string     `json:"company_name,omitempty"` // raison sociale
	Siret          string     `json:"siret,omitempty"`       // 14 chiffres (cle Luhn)
	VatNumber      string     `json:"vat_number,omitempty"`   // TVA intracom (FR...)
	CompanyAddress string     `json:"company_address,omitempty"`
	SiretVerified  bool       `json:"siret_verified"`         // verifie via API INSEE SIRENE
	SiretCheckedAt *time.Time `json:"siret_checked_at,omitempty"`
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

	// Sprint 29 : jetons de verification d email / reinitialisation
	// de mot de passe (memoire, miroir en BDD), anti-spam d envoi et
	// service d e-mails transactionnels (Brevo).
	authTokens   map[string]*authToken // cle : SHA-256 du jeton brut
	sentCooldown map[string]time.Time   // cle : kind + "|" + email
	emails       *Emailer
	// Audit 8 nov. : anti-spam d'INSCRIPTIONS par IP (le throttle
	// existant ne couvrait que le login).
	signupIPs map[string]signupAttempt
}

type loginAttempt struct {
	fails    int
	blockedT time.Time
}

type signupAttempt struct {
	count int
	reset time.Time
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
		authTokens:   make(map[string]*authToken),
		sentCooldown: make(map[string]time.Time),
		signupIPs:    make(map[string]signupAttempt),
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

// validSiret : 14 chiffres + cle de controle Luhn (algorithme SIRET).
func validSiret(s string) bool {
	if len(s) != 14 {
		return false
	}
	sum := 0
	for i := 0; i < 14; i++ {
		d := int(s[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if i%2 == 0 { // positions paires (de gauche, 1er = 0) : x2
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// validVat : format TVA intracommunautaire simplifie (2 lettres pays +
// 8-13 caracteres alphanumeriques). La verification approfondie VIES
// est differee (apres CGU avocat).
func validVat(v string) bool {
	if len(v) < 3 || len(v) > 15 {
		return false
	}
	for i := 0; i < 2; i++ {
		if v[i] < 'A' || v[i] > 'Z' {
			return false
		}
	}
	for i := 2; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

const maxLoginFails = 5
const loginBlockTime = 15 * time.Minute

// Audit du 8 nov. : anti-spam d'inscriptions par IP (fenetre glissante
// d'une heure). Un faux positif possible (IP partagee / NAT) est
// preferable au spam de comptes en bêta publique.
const maxSignupsPerIP = 10
const signupWindow = 1 * time.Hour

// signupBloque enregistre une tentative d'inscription depuis ip et
// indique si elle depasse le quota de la fenetre.
func (a *AccountStore) signupBloque(ip string) bool {
	if ip == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	sc, ok := a.signupIPs[ip]
	if !ok || now.After(sc.reset) {
		a.signupIPs[ip] = signupAttempt{count: 1, reset: now.Add(signupWindow)}
		return false
	}
	sc.count++
	a.signupIPs[ip] = sc
	return sc.count > maxSignupsPerIP
}

// SignUp cree un compte. consent = acceptation des CGU + politique de
// confidentialite (RGPD art. 6/7). Retourne l'utilisateur, la cle API
// (entreprise) ou le jeton de noeud personnel (fournisseur), et le
// jeton de session.
func (a *AccountStore) SignUp(email, password, role, name string, consent bool,
	company *CompanyInfo) (*User, string, string, int) {
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
	ci, code := normalizeCompany(company)
	if code != 0 {
		return nil, "", "", code
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
	if ci != nil {
		u.AccountType = "societe"
		u.CompanyName = ci.Name
		u.Siret = ci.Siret
		u.VatNumber = ci.Vat
		u.CompanyAddress = ci.Address
	} else {
		u.AccountType = "particulier"
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
	if ci != nil {
		go a.verifySiret(u.ID) // verification INSEE SIRENE en tache de fond
	}
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
	consentAt, sessionsBefore time.Time, deletedAt *time.Time,
	accountType, companyName, siret, vat, companyAddr string,
	siretVerified bool, siretCheckedAt *time.Time, emailVerified bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if deletedAt != nil {
		return // compte supprime : rien a recharger
	}
	u := &User{
		ID: id, Email: email, Role: role, DisplayName: display, OrgID: orgID,
		ConsentAt: consentAt, SessionsBefore: sessionsBefore, CreatedAt: consentAt,
		AccountType: accountType, CompanyName: companyName, Siret: siret,
		VatNumber: vat, CompanyAddress: companyAddr,
		SiretVerified: siretVerified, SiretCheckedAt: siretCheckedAt,
		EmailVerified: emailVerified,
	}
	if u.AccountType == "" {
		u.AccountType = "particulier"
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

// ------------------------------------------------------------
// Sprint 29 : verification d adresse e-mail + reinitialisation du
// mot de passe. Jetons a usage unique valables 1 heure (decision
// produit). Seul le SHA-256 du jeton est stocke (BDD et memoire).
// ------------------------------------------------------------

type authToken struct {
	id        string
	userID    string
	kind      string // verify_email | reset_password
	hash      string // SHA-256 hex du jeton brut
	expiresAt time.Time
}

const authTokenTTL = time.Hour
const sendCooldown = time.Minute

// newAuthToken genere un jeton (brut renvoye a l appelant), le
// memorise et le persiste. email sert uniquement a l anti-spam.
func (a *AccountStore) newAuthToken(userID, kind, email string) string {
	raw := randomHex(32)
	t := &authToken{
		id:        "tok-" + randomHex(4),
		userID:    userID,
		kind:      kind,
		hash:      hashKey(raw),
		expiresAt: time.Now().Add(authTokenTTL),
	}
	a.mu.Lock()
	a.authTokens[t.hash] = t
	a.sentCooldown[kind+"|"+email] = time.Now()
	a.mu.Unlock()
	a.store.SaveAuthToken(t.id, t.userID, t.kind, t.hash, t.expiresAt)
	return raw
}

// cooldownRecent : true si un envoi de ce type a deja eu lieu il y a
// moins d une minute (anti-spam de renvoi).
func (a *AccountStore) cooldownRecent(kind, email string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	last, ok := a.sentCooldown[kind+"|"+email]
	return ok && time.Since(last) < sendCooldown
}

// consumeAuthToken verifie un jeton brut (present, bon type, non
// expire, compte vivant) et le consomme : usage unique.
func (a *AccountStore) consumeAuthToken(raw, kind string) *User {
	if raw == "" || len(raw) > 128 {
		return nil
	}
	hash := hashKey(raw)
	a.mu.Lock()
	t, ok := a.authTokens[hash]
	if !ok || t.kind != kind || time.Now().After(t.expiresAt) {
		a.mu.Unlock()
		return nil
	}
	delete(a.authTokens, hash)
	u := a.users[t.userID]
	a.mu.Unlock()
	if u == nil || u.DeletedAt != nil {
		return nil
	}
	a.store.DeleteAuthToken(t.id)
	return u
}

// RestoreAuthToken recharge un jeton actif depuis la BDD (demarrage).
func (a *AccountStore) RestoreAuthToken(id, userID, kind, hash string, expiresAt time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authTokens[hash] = &authToken{
		id: id, userID: userID, kind: kind, hash: hash, expiresAt: expiresAt,
	}
}

// IssueVerification cree et envoie (ou journalise) un jeton de
// verification d adresse. Sans effet si l adresse est deja verifiee.
func (a *AccountStore) IssueVerification(u *User) {
	if u == nil || u.DeletedAt != nil {
		return
	}
	a.mu.RLock()
	verified := u.EmailVerified
	a.mu.RUnlock()
	if verified {
		return
	}
	raw := a.newAuthToken(u.ID, "verify_email", u.Email)
	if a.emails != nil {
		a.emails.SendVerification(u.Email, raw)
	}
}

// UserByEmail : compte vivant par adresse (insensible a la casse).
func (a *AccountStore) UserByEmail(email string) *User {
	email = strings.ToLower(strings.TrimSpace(email))
	a.mu.RLock()
	defer a.mu.RUnlock()
	id, ok := a.byEmail[email]
	if !ok {
		return nil
	}
	return a.users[id]
}

// GET /verify?token=X : clic depuis l e-mail. Page HTML autonome.
func (a *AccountStore) handleVerify(w http.ResponseWriter, r *http.Request) {
	u := a.consumeAuthToken(r.URL.Query().Get("token"), "verify_email")
	if u == nil {
		writeVerifyPage(w, false, "")
		return
	}
	a.mu.Lock()
	u.EmailVerified = true
	a.mu.Unlock()
	a.store.SaveUserEmailVerified(u.ID, true)
	dashLog("email_verified", u.Email+" a verifie son adresse")
	writeVerifyPage(w, true, u.Email)
}

func writeVerifyPage(w http.ResponseWriter, ok bool, email string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tete := "<!DOCTYPE html><html lang=\"fr\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>ComputeShare - Verification</title></head><body style=\"font-family:Arial,Helvetica,sans-serif;background:#f8fafc;color:#0f172a\"><div style=\"max-width:520px;margin:48px auto;background:#ffffff;border-radius:12px;padding:32px;border:1px solid #e2e8f0\">"
	pied := "</div></body></html>"
	if ok {
		fmt.Fprintf(w, tete+"<h2>Adresse verifiee</h2><p>L adresse <b>%s</b> est confirmee. Votre compte est complet : vous pouvez soumettre des jobs.</p><p><a href=\"/login\">Se connecter</a></p>"+pied, email)
		return
	}
	w.Write([]byte(tete + "<h2>Lien invalide ou expire</h2><p>Ce lien de verification a deja ete utilise ou a expire (validite : 1 heure).</p><p>Connectez-vous puis demandez un nouvel e-mail depuis votre console.</p><p><a href=\"/login\">Se connecter</a></p>" + pied))
}

// POST /auth/resend_verification (session) : renvoi de l e-mail de
// verification, anti-spam 1 minute.
func (a *AccountStore) handleResendVerification(w http.ResponseWriter, r *http.Request, u *User) {
	if u.EmailVerified {
		writeJSON(w, map[string]interface{}{"ok": true, "already_verified": true})
		return
	}
	if a.cooldownRecent("verify_email", u.Email) {
		writeErr(w, http.StatusTooManyRequests, "un e-mail vient d etre envoye, reessayez dans une minute")
		return
	}
	raw := a.newAuthToken(u.ID, "verify_email", u.Email)
	if a.emails != nil {
		a.emails.SendVerification(u.Email, raw)
	}
	writeJSON(w, map[string]interface{}{"ok": true})
}

// POST /auth/forgot_password {email} : envoie un lien de
// reinitialisation. Reponse identique que le compte existe ou non
// (aucune divulgation d existence de compte).
func (a *AccountStore) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	u := a.UserByEmail(req.Email)
	if u == nil || a.cooldownRecent("reset_password", u.Email) {
		writeJSON(w, map[string]interface{}{"sent": true})
		return
	}
	raw := a.newAuthToken(u.ID, "reset_password", u.Email)
	if a.emails != nil {
		a.emails.SendReset(u.Email, raw)
	}
	writeJSON(w, map[string]interface{}{"sent": true})
}

// POST /auth/reset_password {token, new_password} : appele par la
// page /reset. Revoque toutes les sessions existantes.
func (a *AccountStore) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	if !validPassword(req.NewPassword) {
		writeErr(w, http.StatusBadRequest, "mot de passe : 8 caracteres minimum")
		return
	}
	u := a.consumeAuthToken(req.Token, "reset_password")
	if u == nil {
		writeErr(w, http.StatusBadRequest, "lien invalide ou expire : redemandez un e-mail")
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
	a.mu.Unlock()
	dashLog("password_reset", u.Email+" a reinitialise son mot de passe")
	writeJSON(w, map[string]bool{"ok": true})
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

// CompanyInfo : infos societe saisies a l'inscription ou plus tard.
type CompanyInfo struct {
	Name    string `json:"name"`
	Siret   string `json:"siret"`
	Vat     string `json:"vat_number"`
	Address string `json:"address"`
}

// normalizeCompany valide les infos societe. Retourne nil pour un
// compte particulier ; un SIRET invalide ou une TVA mal formee
// refusent la demande (400).
func normalizeCompany(c *CompanyInfo) (*CompanyInfo, int) {
	if c == nil {
		return nil, 0
	}
	siret := strings.TrimSpace(c.Siret)
	vat := strings.ToUpper(strings.TrimSpace(c.Vat))
	if siret == "" {
		return nil, 0 // rien de saisi : compte particulier
	}
	if !validSiret(siret) {
		return nil, http.StatusBadRequest
	}
	if vat != "" && !validVat(vat) {
		return nil, http.StatusBadRequest
	}
	return &CompanyInfo{
		Name:    strings.TrimSpace(c.Name),
		Siret:   siret,
		Vat:     vat,
		Address: strings.TrimSpace(c.Address),
	}, 0
}

// Sprint 15d : limitations des comptes particuliers (non societe ou
// societe non verifiee). Valeurs de beta, ajustables.
const (
	maxParticulierTopupEur    = 50.0  // solde max d'une entreprise particuliere
	maxParticulierWithdrawMois = 100.0 // retraits max / 30 jours glissants
	maxParticulierWithdrawTot  = 500.0 // retraits cumules max
)

// isParticulier : vrai si le compte n'est PAS une societe verifiee.
func (u *User) isParticulier() bool {
	return u.AccountType != "societe" || !u.SiretVerified
}

// TopupAllowed : un particulier (societe non verifiee incluse) ne peut
// pas recharger au-dela de maxParticulierTopupEur.
func TopupAllowed(u *User, balanceAfter float64) bool {
	if u == nil || !u.isParticulier() {
		return true
	}
	return balanceAfter <= maxParticulierTopupEur
}

// WithdrawAllowed : un particulier plafonne ses retraits mensuels et
// cumules. Les societes verifiees ne sont pas limitees ici.
func WithdrawAllowed(u *User, last30d, total float64) bool {
	if u == nil || !u.isParticulier() {
		return true
	}
	return last30d <= maxParticulierWithdrawMois && total <= maxParticulierWithdrawTot
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

// UserByOrg retrouve le compte entreprise a partir de son org B2B
// (Sprint 15d : plafond de credits des particuliers).
func (a *AccountStore) UserByOrg(orgID string) *User {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, u := range a.users {
		if u.OrgID == orgID {
			return u
		}
	}
	return nil
}

// UserByID retrouve un compte par son identifiant.
func (a *AccountStore) UserByID(id string) *User {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.users[id]
}

// SetCompany met a jour le statut particulier/societe et les infos
// societe d'un compte (Sprint 15d). company == nil => passage en
// particulier (les donnees societe sont effacees, RGPD).
func (a *AccountStore) SetCompany(userID string, company *CompanyInfo) (*User, int) {
	ci, code := normalizeCompany(company)
	if code != 0 {
		return nil, code
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.users[userID]
	if !ok {
		return nil, http.StatusNotFound
	}
	if ci == nil {
		u.AccountType = "particulier"
		u.CompanyName, u.Siret, u.VatNumber, u.CompanyAddress = "", "", "", ""
		u.SiretVerified = false
		u.SiretCheckedAt = nil
	} else {
		u.AccountType = "societe"
		u.CompanyName, u.Siret, u.VatNumber, u.CompanyAddress =
			ci.Name, ci.Siret, ci.Vat, ci.Address
		u.SiretVerified = false // le nouveau SIRET doit etre reverifie
		u.SiretCheckedAt = nil
	}
	a.store.SaveUserCompany(u)
	if ci != nil {
		go a.verifySiret(userID)
	}
	return u, 0
}

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
	// Audit du 8 nov. : anti-spam d'inscriptions par IP.
	if a.signupBloque(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "trop d'inscriptions depuis cette adresse, reessayez plus tard")
		return
	}
	var req struct {
		Email    string       `json:"email"`
		Password string       `json:"password"`
		Role     string       `json:"role"`
		Name     string       `json:"name"`
		Consent  bool         `json:"consent"`
		Company  *CompanyInfo `json:"company"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	u, secret, session, code := a.SignUp(req.Email, req.Password, req.Role, req.Name, req.Consent, req.Company)
	if u != nil {
		dashLog("signup", u.Email+" a cree un compte "+u.Role)
	}
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
	// Sprint 29 : e-mail de verification de l adresse (asynchrone,
	// pour ne pas ralentir la reponse d inscription).
	go a.IssueVerification(u)

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
	dashLog("login", u.Email+" s'est connecte ("+u.Role+")")
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
				"account_type": u.AccountType,
				"company": map[string]interface{}{
					"name": u.CompanyName, "siret": u.Siret,
					"vat_number": u.VatNumber, "address": u.CompanyAddress,
					"siret_verified": u.SiretVerified,
				},
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

// POST /auth/company : declaration ou mise a jour du statut societe
// (Sprint 15d). Body : {"company": {name, siret, vat_number, address}}
// ou {"company": null} pour repasser en particulier.
func (a *AccountStore) handleCompany(w http.ResponseWriter, r *http.Request, u *User) {
	if r.Method != "POST" {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Company *CompanyInfo `json:"company"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	updated, code := a.SetCompany(u.ID, req.Company)
	if updated == nil {
		writeErr(w, code, "siret ou numero de TVA invalide")
		return
	}
	writeJSON(w, updated)
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
		u.AccountType = "particulier"
		u.CompanyName, u.Siret, u.VatNumber, u.CompanyAddress = "", "", "", ""
		u.SiretVerified = false
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
