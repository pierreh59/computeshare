package main

import "testing"

// Sprint 18 (v2) : les jobs conteneur ne partent que vers les noeuds
// qui ont annonce WSL2Ready, et la redistribution respecte la meme
// contrainte. Les jobs allowlist classiques continuent comme avant.

func TestContainerJobOnlyToReadyNodes(t *testing.T) {
	reg, chunker := newTestDash(t)

	// node-a : WSL2 OK ; node-b : pas de conteneur.
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true)
	reg.Register("node-b", "GPU B", "prosumer", "europe", "fp-b", false)

	job := Job{
		ID: "job-ctr-1", Command: "python", TaskCount: 6, Zone: "europe",
		Runtime: "container", Image: "docker.io/library/python:3.12",
	}
	chunks := chunker.Submit(job, "")
	if chunks == nil {
		t.Fatal("la soumission doit reussir : node-a est pret")
	}
	for _, ch := range chunks {
		if ch.NodeID != "node-a" {
			t.Fatalf("chunk %s envoye a %s, attendu node-a (seul noeud conteneur)",
				ch.ID, ch.NodeID)
		}
		if ch.Runtime != "container" || ch.Image == "" {
			t.Fatalf("le chunk doit heriter runtime=container et l'image")
		}
	}

	// Un job conteneur sans aucun noeud pret est refuse.
	reg2, chunker2 := newTestDash(t)
	reg2.Register("node-c", "GPU C", "prosumer", "europe", "fp-c", false)
	if chunker2.Submit(job, "") != nil {
		t.Fatal("aucun noeud pret : la soumission conteneur doit echouer")
	}

	// Un job allowlist part toujours vers tous les noeuds.
	reg3, chunker3 := newTestDash(t)
	reg3.Register("node-d", "GPU D", "prosumer", "europe", "fp-d", false)
	reg3.Register("node-e", "GPU E", "prosumer", "europe", "fp-e", false)
	jobV1 := Job{ID: "job-v1-1", Command: "echo", TaskCount: 2, Zone: "europe"}
	out := chunker3.Submit(jobV1, "")
	if out == nil || len(out) != 2 {
		t.Fatal("un job allowlist doit se repartir sur les 2 noeuds")
	}
}

// TestPickNodeConteneur : la redistribution d'un chunk conteneur ne
// choisit jamais un noeud sans WSL2, meme si c'est le seul en ligne.
func TestPickNodeConteneur(t *testing.T) {
	online := []Node{
		{ID: "node-raw", Zone: "europe"},
		{ID: "node-wsl", Zone: "europe", WSL2Ready: true},
	}
	ch := &Chunk{ID: "c1", NodeID: "node-raw", Runtime: "container"}
	if got := pickNode(online, ch); got != "node-wsl" {
		t.Fatalf("pickNode doit choisir node-wsl, got %q", got)
	}
	// Sans aucun noeud pret : aucun choix possible.
	online2 := []Node{{ID: "node-raw", Zone: "europe"}}
	if got := pickNode(online2, ch); got != "" {
		t.Fatalf("aucun noeud conteneur : pickNode doit rendre vide, got %q", got)
	}
	// Un chunk allowlist peut aller sur les deux.
	chv1 := &Chunk{ID: "c2", NodeID: "node-x", Zone: "europe"}
	if got := pickNode(online2, chv1); got != "node-raw" {
		t.Fatalf("chunk allowlist doit pouvoir aller sur node-raw, got %q", got)
	}
}
