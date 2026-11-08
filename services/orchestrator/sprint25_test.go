package main

import (
	"testing"
)

// Sprint 25 : un job "gpu": true ne part QUE vers les noeuds gpu_ready.
func TestSubmitGPUFiltreLesNoeuds(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-cpu", "GPU A", "prosumer", "europe", "fp-a", true, false)
	reg.Register("node-gpu", "GPU B", "prosumer", "europe", "fp-b", true, true)

	// Job GPU : les 3 taches doivent toutes aller vers node-gpu.
	job := Job{ID: "job-s25-gpu", Command: "nvidia-smi", TaskCount: 3,
		Runtime: "container", Image: "alpine", Gpu: true}
	chunks := chunker.Submit(job, "")
	if chunks == nil {
		t.Fatal("soumission GPU refusee alors qu'un noeud gpu_ready est en ligne")
	}
	for _, ch := range chunks {
		if ch.NodeID != "node-gpu" {
			t.Fatalf("chunk %s livre a %s, attendu node-gpu (seul noeud gpu_ready)",
				ch.ID, ch.NodeID)
		}
		if !ch.Gpu {
			t.Fatalf("chunk %s doit heriter du flag gpu", ch.ID)
		}
	}
}

// Sprint 25 : aucun noeud gpu_ready => le job GPU est refuse.
func TestSubmitGPUSansNoeudPret(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-cpu", "GPU A", "prosumer", "europe", "fp-a", true, false)
	job := Job{ID: "job-s25-nogpu", Command: "nvidia-smi", TaskCount: 1,
		Runtime: "container", Image: "alpine", Gpu: true}
	if chunker.Submit(job, "") != nil {
		t.Fatal("un job GPU sans noeud gpu_ready doit etre refuse")
	}
}

// Sprint 25 : le tarif gele depend du type de job - tarif CPU pour un
// job CPU (0,03 EUR/h prosumer), tarif GPU pour un job GPU (0,25 EUR/h).
func TestFreezeTarifCPUvsGPU(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, true)

	jobCPU := Job{ID: "job-s25-cpu", Command: "echo", TaskCount: 1, Zone: "europe"}
	if chunker.Submit(jobCPU, "") == nil {
		t.Fatal("soumission CPU refusee")
	}
	ch := chunker.NextFor("node-a")
	if ch == nil {
		t.Fatal("chunk CPU non livre")
	}
	if want := 0.03; ch.PriceEurPerHour != want {
		t.Fatalf("tarif CPU gele = %.4f, attendu %.4f (prosumer CPU)", ch.PriceEurPerHour, want)
	}

	jobGPU := Job{ID: "job-s25-gpup", Command: "nvidia-smi", TaskCount: 1, Gpu: true}
	if chunker.Submit(jobGPU, "") == nil {
		t.Fatal("soumission GPU refusee")
	}
	ch2 := chunker.NextFor("node-a")
	if ch2 == nil {
		t.Fatal("chunk GPU non livre")
	}
	if want := 0.25; ch2.PriceEurPerHour != want {
		t.Fatalf("tarif GPU gele = %.4f, attendu %.4f (prosumer GPU)", ch2.PriceEurPerHour, want)
	}
}

// Sprint 25 : un job CPU est facture au tarif CPU. Un conteneur d'une
// heure sur un noeud consumer verse 0,02 EUR (CPU) au lieu de 0,15 (GPU).
func TestPaiementTarifCPU(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "consumer", "europe", "fp-a", true, true)

	job := Job{ID: "job-s25-pay", Command: "echo", TaskCount: 1, Zone: "europe"}
	if chunker.Submit(job, "") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 3600, 0, 0) {
		t.Fatal("completion refusee")
	}
	if want := 0.02; chunker.wallet.Balance("node-a") != want {
		t.Fatalf("versement CPU = %.6f, attendu %.4f EUR (1 h consumer CPU)",
			chunker.wallet.Balance("node-a"), want)
	}
}

// Sprint 25 : le re-scheduler ne confie jamais un chunk GPU a un noeud
// sans gpu_ready (meme zone).
func TestPickNodeRespecteGPU(t *testing.T) {
	online := []Node{
		{ID: "node-cpu", Zone: "europe", WSL2Ready: true, GpuReady: false},
		{ID: "node-gpu", Zone: "europe", WSL2Ready: true, GpuReady: true},
	}
	ch := &Chunk{ID: "c", Runtime: "container", Gpu: true, NodeID: "node-cpu"}
	if got := pickNode(online, ch); got != "node-gpu" {
		t.Fatalf("pickNode = %s, attendu node-gpu (seul noeud gpu_ready)", got)
	}
	// Le chunk tourne sur node-gpu, le re-scheduler doit rejeter node-cpu.
	ch2 := &Chunk{ID: "c2", Runtime: "container", Gpu: true, NodeID: "node-gpu"}
	online = online[:1] // seul node-cpu est en ligne
	if got := pickNode(online, ch2); got != "" {
		t.Fatalf("pickNode = %s, attendu vide : node-cpu n'est pas gpu_ready", got)
	}
}

// Sprint 25 : les tarifs CPU respectent leurs planchers (SetCPUPrice).
func TestPlanchersCPU(t *testing.T) {
	p := NewPricing(nil)
	if err := p.SetCPUPrice("consumer", 0.010); err == nil {
		t.Fatal("0,010 EUR/h est sous le plancher CPU consumer (0,015)")
	}
	if err := p.SetCPUPrice("consumer", 0.02); err != nil {
		t.Fatalf("0,02 EUR/h doit etre accepte : %v", err)
	}
}
