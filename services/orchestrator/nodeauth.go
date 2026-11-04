package main

// Sprint 14 - Durcissement beta : authentification des noeuds.
// Sprint 15 : en plus du jeton partage (NODE_ENROLLMENT_TOKEN, beta
// fermee), chaque compte vendeur cree via inscription possede un jeton
// personnel (csnode_...). Un tel jeton identifie le noeud comme la
// propriete du compte vendeur (console vendeur, retraits KYC) ;
// le jeton partage reste accepte (noeuds historiques, dev local).
//
// Chaque requete noeud doit porter l'en-tete "X-Node-Token". Le jeton
// partage est defini cote orchestrateur par NODE_ENROLLMENT_TOKEN
// (defaut dev-node-token) et cote client desktop par
// COMPUTERSHARE_NODE_TOKEN.

import (
	"net/http"
	"os"
)

// nodeEnrollmentToken retourne le jeton d'enrolement partage attendu.
func nodeEnrollmentToken() string {
	t := os.Getenv("NODE_ENROLLMENT_TOKEN")
	if t == "" {
		return "dev-node-token"
	}
	return t
}

// NodeAuth : authentificateur des requetes noeuds.
type NodeAuth struct {
	accounts *AccountStore
}

func NewNodeAuth(accounts *AccountStore) *NodeAuth {
	return &NodeAuth{accounts: accounts}
}

// sellerID verifie l'en-tete X-Node-Token. Valeurs de retour :
//   - ("", false) : jeton absent ou invalide -> requete refusee ;
//   - ("", true)  : jeton partage (beta) -> noeud non lie a un compte ;
//   - (id, true)  : jeton personnel -> vendeur identifie.
func (na *NodeAuth) sellerID(r *http.Request) (string, bool) {
	tok := r.Header.Get("X-Node-Token")
	if tok == "" {
		return "", false
	}
	if tok == nodeEnrollmentToken() {
		return "", true
	}
	if id := na.accounts.SellerIDForToken(tok); id != "" {
		return id, true
	}
	return "", false
}

// withNodeToken protege un handler reserve aux noeuds (401 sinon).
// Le handler recoit l'ID du compte vendeur si le jeton est personnel.
func (na *NodeAuth) withNodeToken(h func(w http.ResponseWriter, r *http.Request, sellerID string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sellerID, ok := na.sellerID(r)
		if !ok {
			http.Error(w, "invalid node token", http.StatusUnauthorized)
			return
		}
		h(w, r, sellerID)
	}
}
