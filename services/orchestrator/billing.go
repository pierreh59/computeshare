package main

// Sprint 13 - Billing credits clients B2B (LAI-32).
//
// Modele : le client prepaye des credits (EUR). Chaque chunk termine
// est facture a la GPU-heure REELLE : cout = reversement vendeur x
// marge plateforme. La TVA est journalisee separement (OSS : 20 %
// pour les clients francais, autoliquidation pour les entreprises UE).
// Sous un seuil de credits, une alerte est emise. Les remboursements
// sont de nouveaux evenements (le journal est append-only).
//
// LAI-31 : la console /console consomme ces donnees (solde, couts,
// GPU-heures, journal).

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// Marge plateforme appliquee au reversement vendeur (ADR : le cout
	// client couvre le reversement + la marge + les frais d'exploitation).
	billingMargin = 1.30
	// TVA appliquee aux clients francais (OSS : entreprises UE en
	// autoliquidation, releve du perimetre juridique avant la beta).
	billingVAT = 0.20
	// Sprint 30 : taux de facturation du temps de pull (telechargement
	// de l'image chez le fournisseur). Le client paie ce temps a 20 %
	// du tarif ; le vendeur n'est pas paye pour le pull (sa machine
	// prepare le travail sans consommer de ressource de calcul).
	billingPullRate = 0.20
	// Seuil d'alerte de credits faibles (EUR).
	lowCreditThreshold = 5.0
	// Prix de comparaison AWS (GPU on-demand equivalent, EUR/h GPU).
	awsCompareEurPerHour = 1.00
)

// BillingEvent : ecriture immuable du journal de facturation.
type BillingEvent struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	OrgID     string    `json:"org_id"`
	EventType string    `json:"event_type"` // topup | usage | refund | alert
	AmountEur float64   `json:"amount_eur"` // credit > 0, debit < 0
	VatEur    float64   `json:"vat_eur"`
	GpuHours  float64   `json:"gpu_hours"`
	Ref       string    `json:"ref"`
}

// Billing : journal de facturation + soldes de credits.
type Billing struct {
	mu       sync.Mutex
	store    *Store
	events   []*BillingEvent
	balances map[string]float64
	seq      int64
}

func NewBilling(store *Store) *Billing {
	return &Billing{store: store, balances: make(map[string]float64)}
}

// append ecrit un evenement (memoire + BDD). Mutex deja pris.
func (b *Billing) append(orgID, eventType string, amount, vat, gpuHours float64, ref string) *BillingEvent {
	b.seq++
	e := &BillingEvent{
		Seq: b.seq, At: time.Now(), OrgID: orgID,
		EventType: eventType, AmountEur: amount, VatEur: vat,
		GpuHours: gpuHours, Ref: ref,
	}
	b.events = append(b.events, e)
	b.store.SaveBillingEvent(e)
	b.store.SaveOrgBalance(orgID, b.balances[orgID])
	return e
}

// TopUp credite des credits prepayes (admin, LAI-32).
func (b *Billing) TopUp(orgID string, amountEur float64) (*BillingEvent, error) {
	if amountEur <= 0 {
		return nil, fmt.Errorf("montant de rechargement invalide")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.balances[orgID] += amountEur
	e := b.append(orgID, "topup", amountEur, 0, 0, "")
	return e, nil
}

// Refund rembourse des credits (admin, LAI-32) : nouvelle ecriture
// negative, jamais d'annulation d'historique. Le remboursement RETIRE
// des credits (argent rendu a l'entreprise), il ne peut pas depasser le solde.
func (b *Billing) Refund(orgID string, amountEur float64, ref string) (*BillingEvent, error) {
	if amountEur <= 0 {
		return nil, fmt.Errorf("montant de remboursement invalide")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.balances[orgID] < amountEur {
		return nil, fmt.Errorf("solde insuffisant pour rembourser")
	}
	b.balances[orgID] -= amountEur
	return b.append(orgID, "refund", -amountEur, 0, 0, ref), nil
}

// Balance retourne les credits restants d'une organisation.
func (b *Billing) Balance(orgID string) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.balances[orgID]
}

// HasCredits indique si l'organisation peut soumettre un job
// (au moins 1 centime de credit, sinon on refuse la soumission).
func (b *Billing) HasCredits(orgID string) bool {
	return b.Balance(orgID) > 0.01
}

// ChargeChunk facture un chunk termine : temps de CALCUL a plein
// tarif + temps de pull a taux reduit (Sprint 30). Le GPU-heure
// journalise est le temps de calcul reel (le pull n'est pas du
// calcul). Appele par Chunker.Complete (mutex chunker deja pris).
// Sprint 30 : un chunk de calcul tres court mais avec un pull long
// reste facture (le cout vient aussi du pull).
func (b *Billing) ChargeChunk(orgID string, payoutEur, pullPayoutEur, gpuHours float64, chunkID string) {
	if orgID == "" {
		return
	}
	cost := (payoutEur + pullPayoutEur*billingPullRate) * billingMargin
	if cost <= 0 {
		return
	}
	vat := cost * billingVAT

	b.mu.Lock()
	defer b.mu.Unlock()
	b.balances[orgID] -= cost
	if b.balances[orgID] < 0 {
		log.Printf("billing: ORG %s en credits negatifs (%.2f EUR) "+
			"- les nouveaux jobs seront refuses", orgID, b.balances[orgID])
	}
	b.append(orgID, "usage", -cost, vat, gpuHours, chunkID)

	// Seuil d'alerte : credits faibles (LAI-32).
	if b.balances[orgID] < lowCreditThreshold {
		log.Printf("billing: ALERTE credits faibles pour %s : %.2f EUR restants",
			orgID, b.balances[orgID])
		b.append(orgID, "alert", 0, 0, 0, "low_credits")
	}
}

// JobCost retourne le cout HT total facture pour un job.
func (b *Billing) JobCost(orgID, jobID string) (costEur, vatEur, gpuHours float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	prefix := jobID + "-"
	for _, e := range b.events {
		if e.OrgID == orgID && e.EventType == "usage" &&
			strings.HasPrefix(e.Ref, prefix) {
			costEur += -e.AmountEur
			vatEur += e.VatEur
			gpuHours += e.GpuHours
		}
	}
	return costEur, vatEur, gpuHours
}

// Usage rapporte le compte d'usage d'une organisation (LAI-32) :
// total consomme, TVA, GPU-heures, economie vs AWS equivalente.
func (b *Billing) Usage(orgID string) map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	var spent, vat, hours float64
	var topups int
	for _, e := range b.events {
		if e.OrgID != orgID {
			continue
		}
		switch e.EventType {
		case "usage":
			spent += -e.AmountEur
			vat += e.VatEur
			hours += e.GpuHours
		case "topup":
			topups++
		}
	}
	awsCost := hours * awsCompareEurPerHour
	out := map[string]interface{}{
		"org_id":            orgID,
		"balance_eur":       b.balances[orgID],
		"spent_eur":         spent,
		"vat_eur":           vat,
		"gpu_hours":         hours,
		"topups":            topups,
		"aws_equivalent_eur": awsCost,
	}
	if awsCost > 0 {
		out["savings_pct"] = (1 - spent/awsCost) * 100
	}
	return out
}

// RestoreEvent recharge un evenement de facturation au demarrage.
func (b *Billing) RestoreEvent(e *BillingEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	if e.Seq > b.seq {
		b.seq = e.Seq
	}
}

// RestoreBalance recharge un solde persiste au demarrage.
func (b *Billing) RestoreBalance(orgID string, balance float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.balances[orgID] = balance
}

// OrgIDs liste les organisations ayant un solde de credits connu.
func (b *Billing) OrgIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.balances))
	for id := range b.balances {
		out = append(out, id)
	}
	return out
}

// --- Endpoints ---

// GET /v1/balance : solde de credits de l'organisation authentifiee.
func v1Balance(orgs *OrgStore, b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]interface{}{
			"org_id":      org.ID,
			"balance_eur": b.Balance(org.ID),
		})
	}
}

// GET /v1/usage : rapport d'usage de l'organisation authentifiee.
func v1Usage(orgs *OrgStore, b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, b.Usage(org.ID))
	}
}

// GET /admin/billing : vue globale des credits par organisation.
func adminBilling(b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != "GET" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out := make([]map[string]interface{}, 0)
		for _, orgID := range b.OrgIDs() {
			out = append(out, b.Usage(orgID))
		}
		writeJSON(w, out)
	}
}

// POST /admin/billing/topup : rechargement de credits prepayes.
func adminBillingTopUp(b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			OrgID     string  `json:"org_id"`
			AmountEur float64 `json:"amount_eur"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		e, err := b.TopUp(req.OrgID, req.AmountEur)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, e)
	}
}

// POST /admin/billing/refund : remboursement de credits.
func adminBillingRefund(b *Billing) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			OrgID     string  `json:"org_id"`
			AmountEur float64 `json:"amount_eur"`
			Ref       string  `json:"ref"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		e, err := b.Refund(req.OrgID, req.AmountEur, req.Ref)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dashLog("admin", fmt.Sprintf("remboursement de %.2f EUR pour %s par l'admin (ref: %s)",
			req.AmountEur, req.OrgID, req.Ref))
		writeJSON(w, e)
	}
}
