package main

// Sprint 12 - Wallet vendeur (LAI-34/35/36).
//
// LAI-34 : ledger double-entrée immuable. Chaque gain (payout de chunk)
// et chaque retrait + frais donnent lieu a une ecriture horodatee,
// JAMAIS modifiee ni supprimee. Le solde du wallet derive du ledger
// (source de verite unique). Une reconciliation quotidienne compare le
// total des credits au total des payouts des chunks termines.
//
// LAI-35 : retraits vers PayPal ou gift cards (3 fournisseurs).
// Seuil minimum 5 EUR. Les frais sont calcules et affiches AVANT la
// confirmation (quote), jamais apres coup.
//
// LAI-36 : aucun retrait possible tant que le KYC du vendeur n'est pas
// approuve ; un KYC en cours de controle bloque le wallet.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	minWithdrawalEur = 5.0
	withdrawalIDPrefix = "wd-"
	// Reconciliation toutes les 24 h (LAI-34).
	reconcileInterval = 24 * time.Hour
)

// Fournisseurs de retrait (LAI-35 : 3 gift cards minimum + PayPal).
var withdrawalProviders = map[string]struct {
	Label  string
	FeePct float64 // pourcentage du montant
	FeeMin float64 // frais plancher en EUR
}{
	"paypal":               {Label: "PayPal", FeePct: 3.0, FeeMin: 0.50},
	"gift_card_amazon":     {Label: "Gift card Amazon", FeePct: 0, FeeMin: 0},
	"gift_card_steam":      {Label: "Gift card Steam", FeePct: 0, FeeMin: 0},
	"gift_card_googleplay": {Label: "Gift card Google Play", FeePct: 0, FeeMin: 0},
}

// LedgerEntry : une ecriture immuable du grand livre (LAI-34).
type LedgerEntry struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	NodeID    string    `json:"node_id"`
	EntryType string    `json:"entry_type"` // payout | withdrawal | fee
	AmountEur float64   `json:"amount_eur"` // credit > 0, debit < 0
	Ref       string    `json:"ref"`
}

// Withdrawal : une demande de retrait (LAI-35).
type Withdrawal struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	Provider  string    `json:"provider"`
	AmountEur float64   `json:"amount_eur"`
	FeeEur    float64   `json:"fee_eur"`
	Status    string    `json:"status"` // pending | processed | failed
	CreatedAt time.Time `json:"created_at"`
}

// KYC : statut de verification du vendeur (LAI-36).
type KYC struct {
	NodeID    string     `json:"node_id"`
	Status    string     `json:"status"` // none | pending | approved | rejected
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}

// Wallet : grand livre + retraits + KYC, protege par mutex.
type Wallet struct {
	mu           sync.Mutex
	store        *Store
	ledger       []*LedgerEntry
	withdrawals  map[string]*Withdrawal
	kyc          map[string]*KYC
	seq          int64
}

func NewWallet(store *Store) *Wallet {
	w := &Wallet{
		store:       store,
		withdrawals: make(map[string]*Withdrawal),
		kyc:         make(map[string]*KYC),
	}
	return w
}

// Start lance la boucle de reconciliation apres que le grand livre a
// ete recharge depuis la BDD (sinon la premiere reconciliation
// comparerait un ledger vide avec les chunks passes = faux ecart).
func (w *Wallet) Start() {
	go w.reconcileLoop()
}

// append ecrit une entree au grand livre (memoire + BDD). Appelant doit
// tenir le mutex. Aucune ecriture n'est jamais modifiee ensuite (LAI-34).
func (w *Wallet) append(nodeID, entryType string, amount float64, ref string) *LedgerEntry {
	w.seq++
	e := &LedgerEntry{
		Seq: w.seq, At: time.Now(), NodeID: nodeID,
		EntryType: entryType, AmountEur: amount, Ref: ref,
	}
	w.ledger = append(w.ledger, e)
	w.store.SaveLedger(e)
	return e
}

// CreditPayout enregistre le gain d'un chunk termine (LAI-34).
// Appele par Chunker.Complete, mutex chunker deja pris cote appelant.
func (w *Wallet) CreditPayout(nodeID string, amountEur float64, chunkID string) {
	if amountEur <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.append(nodeID, "payout", amountEur, chunkID)
}

// Balance calcule le solde disponible d'un vendeur (derive du ledger).
func (w *Wallet) Balance(nodeID string) float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balanceLocked(nodeID)
}

func (w *Wallet) balanceLocked(nodeID string) float64 {
	var sum float64
	for _, e := range w.ledger {
		if e.NodeID == nodeID {
			sum += e.AmountEur
		}
	}
	return sum
}

// History retourne les ecritures d'un vendeur (plus recentes d'abord).
func (w *Wallet) History(nodeID string) []*LedgerEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*LedgerEntry, 0)
	for i := len(w.ledger) - 1; i >= 0; i-- {
		if w.ledger[i].NodeID == nodeID {
			out = append(out, w.ledger[i])
		}
	}
	return out
}

// Quote calcule les frais d'un retrait AVANT confirmation (LAI-35).
// Retourne (frais, net, erreur).
func (w *Wallet) Quote(provider string, amountEur float64) (float64, float64, error) {
	p, ok := withdrawalProviders[provider]
	if !ok {
		return 0, 0, fmt.Errorf("fournisseur inconnu: %s", provider)
	}
	if amountEur < minWithdrawalEur {
		return 0, 0, fmt.Errorf("montant minimum de retrait: %.2f EUR",
			minWithdrawalEur)
	}
	fee := amountEur * p.FeePct / 100.0
	if fee < p.FeeMin {
		fee = p.FeeMin
	}
	return fee, amountEur - fee, nil
}

// KYCStatus retourne le statut KYC d'un vendeur ("none" par defaut).
func (w *Wallet) KYCStatus(nodeID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if k, ok := w.kyc[nodeID]; ok {
		return k.Status
	}
	return "none"
}

// RequestWithdrawal cree une demande de retrait (LAI-35) apres les
// controles : KYC approuve (LAI-36), solde suffisant, seuil minimum.
func (w *Wallet) RequestWithdrawal(nodeID, provider string, amountEur float64) (*Withdrawal, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// LAI-36 : KYC obligatoire avant le premier retrait.
	// (mutex deja pris : lecture directe de la map, sans KYCStatus.)
	kycStatus := "none"
	if k, ok := w.kyc[nodeID]; ok {
		kycStatus = k.Status
	}
	switch kycStatus {
	case "approved":
		// OK
	case "pending":
		return nil, fmt.Errorf("KYC en cours de controle: retrait bloque")
	default:
		return nil, fmt.Errorf("KYC requis avant tout retrait")
	}

	fee, _, err := w.Quote(provider, amountEur)
	if err != nil {
		return nil, err
	}

	// Solvabilite : le retrait ne doit jamais faire passer le solde en
	// negatif. On verifie donc montant + frais, pas seulement le montant
	// (bug trouve lors du test reel : 5 EUR de retrait + 0,50 EUR de
	// frais sur un solde de 5,25 EUR laissait le wallet a -0,25 EUR).
	if w.balanceLocked(nodeID) < amountEur+fee {
		return nil, fmt.Errorf("solde insuffisant (montant + frais: %.2f EUR)",
			amountEur+fee)
	}

	id := fmt.Sprintf("%s%d-%d", withdrawalIDPrefix, time.Now().Unix(), w.seq+1)
	wd := &Withdrawal{
		ID: id, NodeID: nodeID, Provider: provider,
		AmountEur: amountEur, FeeEur: fee, Status: "pending",
		CreatedAt: time.Now(),
	}
	w.withdrawals[id] = wd
	w.store.SaveWithdrawal(wd)

	// Ecritures ledger immediates : debit du montant + debit des frais.
	// Le solde disponible reflete le retrait des l'acceptation.
	w.append(nodeID, "withdrawal", -amountEur, id)
	w.append(nodeID, "fee", -fee, id)
	return wd, nil
}

// ProcessWithdrawal marque un retrait traite ou echoue (admin).
func (w *Wallet) ProcessWithdrawal(id string, status string) error {
	if status != "processed" && status != "failed" {
		return fmt.Errorf("statut invalide: %s", status)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	wd, ok := w.withdrawals[id]
	if !ok {
		return fmt.Errorf("retrait inconnu: %s", id)
	}
	wd.Status = status
	w.store.SaveWithdrawal(wd)
	// En cas d'echec du fournisseur, le vendeur est recredite
	// (nouvelle ecriture, jamais d'annulation : le ledger est append-only).
	if status == "failed" {
		w.append(wd.NodeID, "refund", wd.AmountEur+wd.FeeEur, id)
	}
	return nil
}

// Withdrawals liste les retraits d'un vendeur (tous si nodeID vide).
func (w *Wallet) Withdrawals(nodeID string) []*Withdrawal {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Withdrawal, 0)
	for _, wd := range w.withdrawals {
		if nodeID == "" || wd.NodeID == nodeID {
			out = append(out, wd)
		}
	}
	return out
}

// SetKYC met a jour le statut KYC d'un vendeur (admin, LAI-36).
func (w *Wallet) SetKYC(nodeID, status string) error {
	if status != "none" && status != "pending" && status != "approved" && status != "rejected" {
		return fmt.Errorf("statut KYC invalide: %s", status)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	k, ok := w.kyc[nodeID]
	if !ok {
		k = &KYC{NodeID: nodeID, Status: "none"}
		w.kyc[nodeID] = k
	}
	k.Status = status
	k.CheckedAt = &now
	w.store.SaveKYC(k)
	return nil
}

// RestoreLedger recharge le grand livre depuis la BDD au demarrage.
func (w *Wallet) RestoreLedger(e *LedgerEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ledger = append(w.ledger, e)
	if e.Seq > w.seq {
		w.seq = e.Seq
	}
}

// RestoreWithdrawal recharge un retrait connu de la BDD.
func (w *Wallet) RestoreWithdrawal(wd *Withdrawal) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.withdrawals[wd.ID] = wd
}

// RestoreKYC recharge un statut KYC connu de la BDD.
func (w *Wallet) RestoreKYC(k *KYC) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.kyc[k.NodeID] = k
}

// reconcileLoop (LAI-34) : toutes les 24 h, compare le total des credits
// du ledger au total des payouts des chunks termines. Toute difference
// est signalee (log + alerte admin). Un ecart ne modifie jamais le
// ledger : on investigue, on n'ecrase pas.
func (w *Wallet) reconcileLoop() {
	w.reconcile()
	for range time.Tick(reconcileInterval) {
		w.reconcile()
	}
}

func (w *Wallet) reconcile() {
	w.mu.Lock()
	var ledgerPayouts float64
	for _, e := range w.ledger {
		if e.EntryType == "payout" {
			ledgerPayouts += e.AmountEur
		}
	}
	w.mu.Unlock()

	chunkPayouts := w.store.SumChunkPayouts()
	if chunkPayouts != ledgerPayouts {
		log.Printf("wallet RECONCILIATION ECART: ledger=%.6f chunks=%.6f",
			ledgerPayouts, chunkPayouts)
	} else {
		log.Printf("wallet: reconciliation OK (total payouts %.6f EUR)",
			ledgerPayouts)
	}
}

// --- Endpoints HTTP ---

// GET /wallet?node_id=X : solde + dernieres ecritures.
func walletBalance(w *Wallet) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		nodeID := r.URL.Query().Get("node_id")
		if nodeID == "" {
			http.Error(rw, "node_id requis", http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]interface{}{
			"node_id":   nodeID,
			"balance":   w.Balance(nodeID),
			"kyc":       w.KYCStatus(nodeID),
			"history":   w.History(nodeID),
		})
	}
}

// POST /wallet/quote : simulation de frais AVANT confirmation (LAI-35).
func walletQuote(w *Wallet) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Provider  string  `json:"provider"`
			AmountEur float64 `json:"amount_eur"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, "invalid", http.StatusBadRequest)
			return
		}
		fee, net, err := w.Quote(req.Provider, req.AmountEur)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]interface{}{
			"provider":   req.Provider,
			"amount_eur": req.AmountEur,
			"fee_eur":    fee,
			"net_eur":    net,
		})
	}
}

// POST /wallet/withdraw : demande de retrait (LAI-35/36).
func walletWithdraw(w *Wallet) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			NodeID    string  `json:"node_id"`
			Provider  string  `json:"provider"`
			AmountEur float64 `json:"amount_eur"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" {
			http.Error(rw, "invalid", http.StatusBadRequest)
			return
		}
		wd, err := w.RequestWithdrawal(req.NodeID, req.Provider, req.AmountEur)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, wd)
	}
}
