package main

import (
	"os"
	"testing"
	"time"
)

// newTestStack35 : pile minimale pour les tests du re-scheduler
// sprint 35 (Store memoire, pas de Postgres).
func newTestStack35(t *testing.T) (*Registry, *Chunker) {
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

// Bug n.1 du 11 nov. : un noeud redemarre en plein job laisse un
// chunk orphelin "running" a vie. AVANT le sprint 35, le
// re-scheduler exigeait un noeud hors ligne pour re-distribuer, et
// pickNode refusait de rendre le chunk a son propre noeud : avec un
// seul noeud, le chunk etait bloque pour toujours.
// APRES : un chunk "running" dont le timeout (+60 s de marge) est
// expire est remis en file, meme si son noeud est EN LIGNE, et le
// noeud d'origine peut le reprendre en dernier recours.
func TestRescheduleOrphanNodeOnline(t *testing.T) {
	reg, chunker := newTestStack35(t)
	reg.Register("node-35a", "RTX 5070", "consumer", "europe", "fp-35a", true, true)

	chunks := chunker.Submit(Job{
		ID:        "job-s35-orphan",
		Command:   "python",
		Args:      []string{"-c", "print(1)"},
		TaskCount: 1,
		TimeoutSec: 300,
		Runtime:   "container",
		Image:     "pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime",
		Gpu:       true,
		Zone:      "europe",
	}, "")
	if len(chunks) != 1 {
		t.Fatalf("Submit: %d chunks, attendu 1", len(chunks))
	}
	ch := chunks[0]
	if ch.NodeID != "node-35a" {
		t.Fatalf("chunk attribue a %s, attendu node-35a", ch.NodeID)
	}
	// Le noeud demarre le job puis plante (redemarrage reel du 11 nov.).
	if got := chunker.NextFor("node-35a"); got == nil {
		t.Fatal("NextFor: aucun chunk livre")
	}
	if ch.Status != "running" || ch.Attempts != 1 {
		t.Fatalf("apres NextFor: status=%s attempts=%d, attendu running/1", ch.Status, ch.Attempts)
	}
	// Le noeud revient EN LIGNE mais ne rapportera jamais son chunk
	// (son app ne garde aucun etat des chunks d'une session precedente).
	ch.StartedAt = time.Now().Add(-1 * time.Hour)

	chunker.reschedulePass()

	if ch.Status != "queued" {
		t.Fatalf("chunk orphelin toujours %s, attendu queued", ch.Status)
	}
	if ch.NodeID != "node-35a" {
		t.Fatalf("chunk re-attribue a %s, attendu node-35a (dernier recours)", ch.NodeID)
	}
	// Le noeud unique peut re-prendre son chunk et le rejouer.
	got := chunker.NextFor("node-35a")
	if got == nil || got.ID != ch.ID {
		t.Fatal("NextFor apres re-file: le noeud unique n'a pas recupere son chunk")
	}
	if ch.Attempts != 2 {
		t.Fatalf("attempts=%d apres re-livraison, attendu 2", ch.Attempts)
	}
}

// Sprint 22 conserve : un chunk en cours d'execution dont la grace
// period (timeout + 60 s) n'est pas ecoulee n'est PAS re-distribue,
// meme si le re-scheduler passe (le noeud peut juste etre occupe).
func TestRescheduleGracePasEcoulee(t *testing.T) {
	reg, chunker := newTestStack35(t)
	reg.Register("node-35b", "RTX 5070", "consumer", "europe", "fp-35b", true, true)

	chunks := chunker.Submit(Job{
		ID:        "job-s35-grace",
		Command:   "python",
		Args:      []string{"-c", "print(1)"},
		TaskCount: 1,
		TimeoutSec: 300,
		Runtime:   "container",
		Image:     "alpine",
		Zone:      "europe",
	}, "")
	ch := chunks[0]
	if chunker.NextFor("node-35b") == nil {
		t.Fatal("NextFor: aucun chunk livre")
	}
	// Demarre depuis 100 s seulement : grace = 300 + 60 = 360 s.
	ch.StartedAt = time.Now().Add(-100 * time.Second)

	chunker.reschedulePass()

	if ch.Status != "running" {
		t.Fatalf("chunk en grace re-file (%s), attendu running", ch.Status)
	}
	if chunker.NextFor("node-35b") != nil {
		t.Fatal("NextFor: un chunk en cours ne doit pas etre re-livre pendant la grace")
	}
}

// Un chunk qui a epuise ses tentatives passe a "failed" au lieu de
// boucleter a l'infini sur un noeud qui plante systematiquement.
func TestRescheduleMaxAttempts(t *testing.T) {
	reg, chunker := newTestStack35(t)
	reg.Register("node-35c", "RTX 5070", "consumer", "europe", "fp-35c", true, true)

	chunks := chunker.Submit(Job{
		ID:        "job-s35-max",
		Command:   "python",
		Args:      []string{"-c", "print(1)"},
		TaskCount: 1,
		TimeoutSec: 300,
		Runtime:   "container",
		Image:     "alpine",
		Zone:      "europe",
	}, "")
	ch := chunks[0]
	if chunker.NextFor("node-35c") == nil {
		t.Fatal("NextFor: aucun chunk livre")
	}
	ch.Attempts = maxChunkAttempts
	ch.StartedAt = time.Now().Add(-1 * time.Hour)

	chunker.reschedulePass()

	if ch.Status != "failed" {
		t.Fatalf("chunk a tentatives epuisees toujours %s, attendu failed", ch.Status)
	}
}

// Sprint 35 : pickNode prefere un AUTRE noeud, mais rend le chunk a
// son propre noeud en dernier recours. Les contraintes conteneur
// (WSL2) et GPU restent bloquantes dans les trois boucles.
func TestPickNodeMemeNoeudDernierRecours(t *testing.T) {
	base := Node{ID: "n-a", Zone: "europe", WSL2Ready: true, GpuReady: true, Online: true}
	ch := &Chunk{NodeID: "n-a", Zone: "europe", Runtime: "container", Gpu: true}

	// 1. Un autre noeud de la meme zone est prefere.
	autre := base
	autre.ID = "n-b"
	if got := pickNode([]Node{base, autre}, ch); got != "n-b" {
		t.Fatalf("pickNode a rendu %s, attendu n-b (autre noeud prefere)", got)
	}

	// 2. Noeud unique : le noeud d'origine reprend son chunk.
	if got := pickNode([]Node{base}, ch); got != "n-a" {
		t.Fatalf("pickNode a rendu %q, attendu n-a (dernier recours)", got)
	}

	// 3. Contrainte conteneur : sans WSL2, jamais.
	sansWsl := base
	sansWsl.ID = "n-b"
	sansWsl.WSL2Ready = false
	if got := pickNode([]Node{base, sansWsl}, ch); got != "n-a" {
		t.Fatalf("pickNode a rendu %q, attendu n-a (n-b sans WSL2 refuse)", got)
	}

	// 4. Contrainte GPU : sans gpu_ready, jamais.
	sansGpu := base
	sansGpu.ID = "n-b"
	sansGpu.GpuReady = false
	if got := pickNode([]Node{base, sansGpu}, ch); got != "n-a" {
		t.Fatalf("pickNode a rendu %q, attendu n-a (n-b sans GPU refuse)", got)
	}

	// 5. Personne de compatible : chaine vide.
	unreachable := base
	unreachable.ID = "n-z"
	unreachable.WSL2Ready = false
	unreachable.GpuReady = false
	if got := pickNode([]Node{unreachable}, ch); got != "" {
		t.Fatalf("pickNode a rendu %q, attendu vide (aucun noeud compatible)", got)
	}
}
