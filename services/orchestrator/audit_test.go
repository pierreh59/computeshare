package main

import (
	"testing"
	"time"
)

// Audit 8 nov. : anti-spam d'inscriptions par IP. 10 inscriptions
// acceptees depuis une meme IP pendant la fenetre, la 11e est bloquee,
// et une autre IP n'est pas impactee.
func TestSignupAntiSpamParIP(t *testing.T) {
	store := NewStore()
	orgs := NewOrgStore(store)
	a := NewAccountStore(store, orgs)

	for i := 1; i <= 10; i++ {
		if a.signupBloque("203.0.113.9") {
			t.Fatalf("inscription %d refusee trop tot (quota %d)", i, maxSignupsPerIP)
		}
	}
	if !a.signupBloque("203.0.113.9") {
		t.Fatal("la 11e inscription depuis la meme IP doit etre bloquee")
	}
	if a.signupBloque("198.51.100.7") {
		t.Fatal("une autre IP ne doit pas etre impactee")
	}

	// Apres expiration de la fenetre, l'IP est de nouveau autorisee.
	a.mu.Lock()
	sc := a.signupIPs["203.0.113.9"]
	sc.reset = time.Now().Add(-time.Minute)
	a.signupIPs["203.0.113.9"] = sc
	a.mu.Unlock()
	if a.signupBloque("203.0.113.9") {
		t.Fatal("l IP doit etre reautorisee apres expiration de la fenetre")
	}
}
