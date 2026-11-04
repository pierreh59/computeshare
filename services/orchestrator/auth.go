package main

// LAI-29 - Auth clients B2B : organisations + cles API.
// MVP : une cle par organisation, generee par l'admin, stockee en
// SHA-256 (jamais en clair). Authentification via l'en-tete
// "Authorization: Bearer <cle>".
// Sprint 8 suite : les organisations sont persistees dans PostgreSQL.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Org : organisation cliente B2B.
type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// OrgStore : organisations connues + index cle hachee -> org.
type OrgStore struct {
	mu          sync.RWMutex
	orgs        map[string]*Org
	keys        map[string]string
	store       *Store
	accountAuth func(r *http.Request) *User // sessions des comptes clients (Sprint 15)
}

func NewOrgStore(store *Store) *OrgStore {
	return &OrgStore{
		orgs:  make(map[string]*Org),
		keys:  make(map[string]string),
		store: store,
	}
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateOrg cree une organisation et sa cle API.
// La cle n'est montree qu'une fois, a la creation.
func (o *OrgStore) CreateOrg(name string) (*Org, string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	org := &Org{
		ID:        "org-" + hex.EncodeToString(buf[:4]),
		Name:      name,
		CreatedAt: time.Now(),
	}
	key := "cshare_" + hex.EncodeToString(buf)
	keyHash := hashKey(key)

	o.mu.Lock()
	defer o.mu.Unlock()
	o.orgs[org.ID] = org
	o.keys[keyHash] = org.ID
	o.store.SaveOrg(org, keyHash)
	return org, key, nil
}

// Restore recharge une organisation connue de la BDD.
func (o *OrgStore) Restore(id, name, keyHash string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orgs[id] = &Org{ID: id, Name: name}
	o.keys[keyHash] = id
}

// Authenticate verifie l'en-tete Bearer et retourne l'organisation
// concerne (nil si absente ou invalide).
func (o *OrgStore) Authenticate(r *http.Request) *Org {
	authHeader := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return nil
	}
	hash := hashKey(strings.TrimPrefix(authHeader, prefix))
	o.mu.RLock()
	id, ok := o.keys[hash]
	var org *Org
	if ok {
		org = o.orgs[id]
	}
	auth := o.accountAuth
	o.mu.RUnlock()
	if ok {
		return org
	}
	// Sprint 15 : session d'un compte client -> son organisation.
	if auth != nil {
		if u := auth(r); u != nil && u.OrgID != "" {
			o.mu.RLock()
			org := o.orgs[u.OrgID]
			o.mu.RUnlock()
			return org
		}
	}
	return nil
}
// SetAccountAuth : la console web des comptes clients s'authentifie
// par session (Sprint 15) au lieu de la cle API. Injecte le resolver
// de sessions ; sans lui, seule la cle API fonctionne (comportement
// historique des clients B2B pur API).
func (o *OrgStore) SetAccountAuth(f func(r *http.Request) *User) {
	o.mu.Lock()
	o.accountAuth = f
	o.mu.Unlock()
}
