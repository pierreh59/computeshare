package main

// Sprint 17 (LAI-63) - tests du dashboard : blocage/deblocage d'un
// noeud, exclusion de la distribution, redistribution de ses chunks
// en file, journal d'evenements.

import (
	"os"
	"testing"
	"time"
)

func newTestDash(t *testing.T) (*Registry, *Chunker) {
	t.Helper()
	os.Setenv("DATABASE_URL", "")
	store := NewStore()
	fraud := NewFraud()
	reg := NewRegistry(store, fraud)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(reg, pricing, store, fraud, wallet, billing)
	return reg, chunker
}

func TestBanNodeExcluDistribution(t *testing.T) {
	reg, chunker := newTestDash(t)

	// Deux noeuds en ligne.
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false)
	reg.Register("node-b", "GPU B", "prosumer", "europe", "fp-b", false)
	if len(reg.OnlineNodes()) != 2 {
		t.Fatalf("2 noeuds en ligne attendus, %d", len(reg.OnlineNodes()))
	}

	// Un job de 2 taches reparties sur les deux noeuds.
	job := Job{ID: "job-x", Command: "echo", TaskCount: 2, Zone: "europe"}
	if chunker.Submit(job, "") == nil {
		t.Fatal("la soumission doit reussir avec 2 noeuds en ligne")
	}

	// Blocage de node-a : il sort de la distribution.
	if !reg.BanNode("node-a") {
		t.Fatal("le blocage doit reussir")
	}
	if len(reg.OnlineNodes()) != 1 {
		t.Fatalf("le noeud bloque ne doit plus etre distribue, %d restant", len(reg.OnlineNodes()))
	}
	if reg.IsOnline("node-a") {
		t.Error("un noeud bloque doit etre considere hors ligne (re-scheduling)")
	}
	if reg.Heartbeat("node-a") {
		t.Error("le heartbeat d'un noeud bloque doit etre rejete")
	}

	// Les chunks en file de node-a partent sur node-b.
	if n := chunker.ReassignNode("node-a"); n != 1 {
		t.Fatalf("1 chunk devait etre redistribue, %d", n)
	}
	for _, w := range chunker.WorkOf("node-b") {
		_ = w
	}
	if got := chunker.WorkOf("node-a"); len(got) != 0 {
		t.Errorf("node-a ne doit plus avoir de travail, %v", got)
	}
	if got := chunker.WorkOf("node-b"); len(got) != 2 {
		t.Errorf("node-b doit avoir recupere les 2 chunks, %v", got)
	}

	// Un noeud bloque ne peut pas redevenir en ligne via Register.
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false)
	if reg.IsOnline("node-a") {
		t.Error("un register d'un noeud bloque ne doit pas le remettre en ligne")
	}

	// Deblocage : il revient avec une confiance de 50.
	if !reg.UnbanNode("node-a") {
		t.Fatal("le deblocage doit reussir")
	}
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false)
	if !reg.IsOnline("node-a") {
		t.Error("apres deblocage, le register doit remettre le noeud en ligne")
	}
	if reg.TrustOf("node-a") != 50 {
		t.Errorf("confiance attendue 50 apres deblocage, %.0f", reg.TrustOf("node-a"))
	}
}

func TestDashLogEtRecent(t *testing.T) {
	dashLog("test", "message un")
	dashLog("test", "message deux")
	ev := dashRecent(10)
	if len(ev) < 2 {
		t.Fatalf("2 evenements minimum attendus, %d", len(ev))
	}
	// Le plus recent d'abord.
	if ev[0].Msg != "message deux" {
		t.Errorf("le plus recent d'abord attendu, recu %q", ev[0].Msg)
	}
	if time.Since(ev[0].At) > time.Minute {
		t.Error("l'horodatage doit etre recent")
	}
}
