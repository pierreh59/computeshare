package main

import "testing"

// TestRefundRetireCredits : un remboursement doit DEBITER le solde,
// pas le crediter (bug corrigie : refund faisait += comme un topup).
func TestRefundRetireCredits(t *testing.T) {
	store := &Store{}
	b := NewBilling(store)
	if _, err := b.TopUp("org-x", 10); err != nil {
		t.Fatal(err)
	}
	e, err := b.Refund("org-x", 4, "test")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Balance("org-x"); got != 6 {
		t.Fatalf("solde apres remboursement = %.2f, attendu 6.00", got)
	}
	if e.AmountEur >= 0 {
		t.Fatalf("evenement refund doit etre negatif, got %.2f", e.AmountEur)
	}
	// Rembourser plus que le solde doit etre refuse.
	if _, err := b.Refund("org-x", 100, "trop"); err == nil {
		t.Fatal("remboursement superieur au solde doit echouer")
	}
	if got := b.Balance("org-x"); got != 6 {
		t.Fatalf("solde doit rester 6.00 apres echec, got %.2f", got)
	}
}
