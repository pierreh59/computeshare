package main

// Sprint 14 - Durcissement beta : authentification des noeuds.
//
// Avant : n'importe qui connaissant l'URL pouvait se declarer noeud,
// reclamer des chunks et remonter des resultats (avec le node_id de
// son choix, y compris celui d'un autre vendeur).
//
// Maintenant : chaque requete noeud doit porter l'en-tete
// "X-Node-Token: <jeton d'enrôlement>". Le jeton est defini cote
// orchestrateur par la variable d'environnement NODE_ENROLLMENT_TOKEN
// (defaut dev-node-token pour le developpement local) et cote client
// desktop par COMPUTERSHARE_NODE_TOKEN.
//
// Un vrai protocole d'enrôlement par noeud (jeton individuel, rotation,
// revocation) reste a cadrer avant l'ouverture publique ; ce jeton
// partage ferme deja la porte aux curieux et aux faux noeuds pendant
// la beta fermee.

import (
	"net/http"
	"os"
)

// nodeEnrollmentToken retourne le jeton d'enrolement attendu.
func nodeEnrollmentToken() string {
	t := os.Getenv("NODE_ENROLLMENT_TOKEN")
	if t == "" {
		return "dev-node-token"
	}
	return t
}

// requireNodeToken verifie l'en-tete X-Node-Token.
func requireNodeToken(r *http.Request) bool {
	return r.Header.Get("X-Node-Token") == nodeEnrollmentToken()
}

// withNodeToken protege un handler reserve aux noeuds (401 sinon).
func withNodeToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireNodeToken(r) {
			http.Error(w, "invalid node token", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}
