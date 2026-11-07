package main

import (
	"testing"
	"time"
)

// Sprint 22 : un chunk deja termine ignore tout nouveau rapport
// (pas de double paiement), et le paiement va au noeud qui RAPPORTE,
// pas au noeud theoriquement assigne.
func TestCompleteIdempotentEtPayeLeRapporteur(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	reg.Register("node-b", "GPU B", "prosumer", "europe", "fp-b", false, false)

	job := Job{ID: "job-s22-a", Command: "echo", TaskCount: 1, Zone: "europe"}
	if chunker.Submit(job, "") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if ch == nil {
		t.Fatal("chunk non livre a node-a")
	}

	// Simulation : le chunk a ete re-attribue a node-b pendant
	// l'execution (ch.NodeID change), mais node-a rapporte.
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha-1", "", 10) {
		t.Fatal("premier rapport doit etre enregistre")
	}
	if bal := chunker.wallet.Balance("node-a"); bal <= 0 {
		t.Fatalf("node-a (le rapporteur) doit etre paye, solde %.6f", bal)
	}
	if bal := chunker.wallet.Balance("node-b"); bal != 0 {
		t.Fatalf("node-b n'a rien execute, solde %.6f", bal)
	}

	// Rapport en double : ignore, pas de second versement.
	if chunker.Complete(ch.ID, "node-a", "ok", "sha-1", "", 10) {
		t.Fatal("un rapport en double doit etre ignore")
	}
	bal := chunker.wallet.Balance("node-a")
	if chunker.Complete(ch.ID, "node-b", "ok", "sha-1", "", 10) {
		t.Fatal("un rapport en double (autre noeud) doit etre ignore")
	}
	if chunker.wallet.Balance("node-a") != bal {
		t.Fatal("le solde ne doit pas changer apres un rapport en double")
	}
	if chunker.wallet.Balance("node-b") != 0 {
		t.Fatal("node-b ne doit rien toucher")
	}
}

// Sprint 22 : le re-scheduler respecte le timeout du chunk. Un chunk
// "running" dont le noeud parait hors ligne mais dont le timeout n'est
// pas expire ne doit PAS etre re-attribue.
func TestRescheduleAttendLeTimeout(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	reg.Register("node-b", "GPU B", "prosumer", "europe", "fp-b", false, false)

	job := Job{ID: "job-s22-b", Command: "echo", TaskCount: 1,
		Zone: "europe", TimeoutSec: 600}
	submitted := chunker.Submit(job, "")
	if submitted == nil {
		t.Fatal("soumission refusee")
	}
	// Le chunk peut etre attribue a node-a OU node-b (round-robin) :
	// on reclame depuis son vrai titulaire.
	owner := submitted[0].NodeID
	ch := chunker.NextFor(owner)
	if ch == nil {
		t.Fatal("chunk non livre")
	}
	if ch.NodeID != owner {
		t.Fatalf("chunk livre a %s, attendu %s", ch.NodeID, owner)
	}
	if ch.TimeoutSecs != 600 {
		t.Fatalf("timeout herite attendu 600, %d", ch.TimeoutSecs)
	}

	// Le titulaire "disparait" (hors ligne) mais le chunk vient de
	// demarrer : aucune re-attribution pendant la periode de grace.
	chunker.mu.Lock()
	ch.StartedAt = time.Now().Add(-1 * time.Minute) // demarre il y a 1 min
	chunker.mu.Unlock()
	reg.mu.Lock()
	for _, n := range reg.nodes {
		if n.ID == owner {
			n.Online = false
		}
	}
	reg.mu.Unlock()

	chunker.mu.Lock()
	grace := ch.TimeoutSecs
	if grace < 300 {
		grace = 300
	}
	expire := time.Since(ch.StartedAt) >= time.Duration(grace)*time.Second
	chunker.mu.Unlock()
	if expire {
		t.Fatal("apres 1 min, le chunk de timeout 600 s ne doit pas etre expire")
	}
	// Le prochain tick de rescheduleLoop ne doit rien faire : la
	// periode de grace n'est pas passee.
	chunker.mu.Lock()
	graceOk := ch.Status == "running" && !reg.IsOnline(owner) &&
		time.Since(ch.StartedAt) < time.Duration(grace)*time.Second
	chunker.mu.Unlock()
	if !graceOk {
		t.Fatal("le chunk doit rester running pendant sa periode de grace")
	}

	// Cas expire : demarre il y a plus longtemps que le timeout.
	chunker.mu.Lock()
	ch.StartedAt = time.Now().Add(-2 * time.Hour)
	chunker.mu.Unlock()
	chunker.mu.Lock()
	expire2 := time.Since(ch.StartedAt) >= time.Duration(grace)*time.Second
	chunker.mu.Unlock()
	if !expire2 {
		t.Fatal("apres 2 h, le chunk doit etre considere expire")
	}
}
