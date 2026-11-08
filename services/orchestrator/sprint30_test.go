package main

import (
	"math"
	"testing"
)

// Sprint 30 : le vendeur est paye UNIQUEMENT sur le temps de calcul.
// Un chunk de 15 min de pull + 10 min de calcul verse 10 min au tarif
// gele (pas 25 min : le pull n'est jamais paye au vendeur).
func TestPullNonPayeAuVendeur(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, true)

	job := Job{ID: "job-s30-pay", Command: "echo", TaskCount: 1, Gpu: true}
	if chunker.Submit(job, "org-x") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if ch == nil {
		t.Fatal("chunk non livre")
	}
	// 15 min de pull (900 000 ms) + 10 min de calcul (600 000 ms),
	// duree totale rapportee 1500 s.
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 1500, 900000, 600000) {
		t.Fatal("completion refusee")
	}
	want := 0.25 * (600.0 / 3600.0) // 10 min au tarif GPU prosumer
	if got := chunker.wallet.Balance("node-a"); math.Abs(got-want) > 1e-9 {
		t.Fatalf("versement = %.6f, attendu %.6f (10 min de calcul seulement, pull non paye)",
			got, want)
	}
}

// Sprint 30 : le client paie le calcul a plein tarif + le pull a 20 %
// du tarif (marge plateforme incluse), et les GPU-heures journalisees
// ne comptent QUE le temps de calcul.
func TestPullFactureAuClientA20Pourcent(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, true)

	if _, errTop := chunker.billing.TopUp("org-y", 10); errTop != nil {
		t.Fatal("topup refuse")
	}
	job := Job{ID: "job-s30-bill", Command: "echo", TaskCount: 1, Gpu: true}
	if chunker.Submit(job, "org-y") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 1500, 900000, 600000) {
		t.Fatal("completion refusee")
	}
	payout := 0.25 * (600.0 / 3600.0)  // calcul : valeur tarif vendeur
	pullVal := 0.25 * (900.0 / 3600.0) // pull : valeur tarif vendeur
	want := 10 - (payout+pullVal*0.20)*1.30
	got := chunker.billing.Balance("org-y")
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("solde client = %.6f, attendu %.6f (calcul plein tarif + pull a 20 pourcent)",
			got, want)
	}
	// Les GPU-heures facturees = temps de CALCUL seul.
	_, _, hours := chunker.billing.JobCost("org-y", "job-s30-bill")
	wantHours := 600.0 / 3600.0
	if math.Abs(hours-wantHours) > 1e-9 {
		t.Fatalf("gpu_hours = %.6f, attendu %.6f (temps de calcul, pas le pull)",
			hours, wantHours)
	}
}

// Sprint 30 : un chunk de calcul tres court mais avec un pull long
// reste facture au client (le cout vient aussi du pull a 20 %).
func TestPullSeulResteFacture(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", true, true)

	if _, errTop := chunker.billing.TopUp("org-w", 10); errTop != nil {
		t.Fatal("topup refuse")
	}
	job := Job{ID: "job-s30-pull", Command: "echo", TaskCount: 1, Gpu: true}
	if chunker.Submit(job, "org-w") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	// Calcul instantane (200 ms) mais 15 min de pull.
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 900, 900000, 200) {
		t.Fatal("completion refusee")
	}
	pullVal := 0.25 * (900.0 / 3600.0)
	want := 10 - (0.25*(200.0/3_600_000.0)+pullVal*0.20)*1.30
	got := chunker.billing.Balance("org-w")
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("solde client = %.6f, attendu %.6f (le pull seul doit etre facture)",
			got, want)
	}
}

// Sprint 30 : un rapport SANS compute_ms (ancien desktop, jobs
// sandbox) facture tout le temps rapporte comme du calcul -
// comportement historique preserve.
func TestFallbackSansComputeMS(t *testing.T) {
	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "consumer", "europe", "fp-a", true, true)

	job := Job{ID: "job-s30-old", Command: "echo", TaskCount: 1}
	if chunker.Submit(job, "org-z") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 3600, 0, 0) {
		t.Fatal("completion refusee")
	}
	if want := 0.02; chunker.wallet.Balance("node-a") != want {
		t.Fatalf("versement = %.6f, attendu %.4f (1 h CPU consumer, fallback)",
			chunker.wallet.Balance("node-a"), want)
	}
}
