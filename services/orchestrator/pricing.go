package main

// LAI-51 - Moteur de tarification dynamique (ADR-006).
// LAI-52 - Garde-fous : plancher par categorie + alerte churn.
// Les prix sont pilotables par l'admin, MAIS :
//   - jamais sous le plancher anti-churn,
//   - une baisse brutale en 24 h genere une alerte churn,
//   - le tarif est gele au moment ou le chunk est livre : jamais retroactif.
// Sprint 8 - Les changements de prix sont persists dans PostgreSQL.

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Planchers de reversement en EUR / GPU-heure (anti-churn).
var FLOORS = map[string]float64{
	"consumer": 0.12,
	"prosumer": 0.20,
	"high_end": 0.30,
}

// Sprint 25 - Tarifs CPU separes (EUR / heure de conteneur 2 vCPU).
// Le GPU dans le conteneur n'etait pas expose avant ce sprint : un
// job CPU etait facture au tarif GPU (incoherence). Un job sans
// "gpu": true est maintenant facture au tarif CPU, beaucoup moins
// cher, calibre sur le marche (Salad 0,009 EUR/h pour 2 vCPU en
// prix client, AWS t3.medium 0,039 EUR/h ; nous nous positionnons
// entre les deux, sous AWS).
var CPU_FLOORS = map[string]float64{
	"consumer": 0.015,
	"prosumer": 0.02,
	"high_end": 0.03,
}

// Garde-fou churn : baisse de plus de 20 % sur 24 h = alerte.
const churnDropThreshold = 0.20
const churnWindow = 24 * time.Hour

type PriceChange struct {
	Category string
	OldEur   float64
	NewEur   float64
	At       time.Time
}

type Alert struct {
	Type string    `json:"type"`
	Msg  string    `json:"msg"`
	At   time.Time `json:"at"`
}

type Pricing struct {
	mu         sync.RWMutex
	current    map[string]float64
	cpuCurrent map[string]float64
	history    []PriceChange
	alerts     []Alert
	store      *Store
}

func NewPricing(store *Store) *Pricing {
	return &Pricing{
		current: map[string]float64{
			"consumer": 0.15,
			"prosumer": 0.25,
			"high_end": 0.35,
		},
		// Sprint 25 : tarifs CPU (payout fournisseur, EUR/h).
		cpuCurrent: map[string]float64{
			"consumer": 0.02,
			"prosumer": 0.03,
			"high_end": 0.04,
		},
		store: store,
	}
}

// CurrentPrice renvoie le tarif GPU courant d'une categorie (0 si inconnue).
func (p *Pricing) CurrentPrice(category string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current[category]
}

// Sprint 25 : tarif CPU courant d'une categorie (0 si inconnue).
func (p *Pricing) CurrentCPUPrice(category string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cpuCurrent[category]
}

// Restore recharge un tarif depuis la BDD au demarrage.
// Sprint 25 : les categories prefixees "cpu:" designent les tarifs CPU.
func (p *Pricing) Restore(category string, eurPerHour float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.HasPrefix(category, "cpu:") {
		p.cpuCurrent[strings.TrimPrefix(category, "cpu:")] = eurPerHour
		return
	}
	p.current[category] = eurPerHour
}

// Snapshot copie des tarifs courants (pour l'admin).
func (p *Pricing) Snapshot() map[string]float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]float64, len(p.current))
	for k, v := range p.current {
		out[k] = v
	}
	return out
}

// Sprint 25 : snapshot des tarifs CPU (pour l'admin).
func (p *Pricing) SnapshotCPU() map[string]float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]float64, len(p.cpuCurrent))
	for k, v := range p.cpuCurrent {
		out[k] = v
	}
	return out
}

// Alerts copie des alertes, plus recentes en premier.
func (p *Pricing) Alerts() []Alert {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Alert, 0, len(p.alerts))
	for i := len(p.alerts) - 1; i >= 0; i-- {
		out = append(out, p.alerts[i])
	}
	return out
}

// priceAgoLocked : tarif en vigueur il y a 'ago' (scan du journal).
func (p *Pricing) priceAgoLocked(category string, ago time.Duration) float64 {
	limit := time.Now().Add(-ago)
	price := 0.0
	seen := false
	for _, ch := range p.history {
		if ch.Category != category {
			continue
		}
		if ch.At.Before(limit) {
			price = ch.NewEur
			seen = true
		} else if !seen {
			price = ch.OldEur // tarif avant le 1er changement de la fenetre
			seen = true
		}
	}
	if !seen {
		price = p.current[category]
	}
	return price
}

// SetPrice applique un nouveau tarif, sous reserve des garde-fous (LAI-52).
func (p *Pricing) SetPrice(category string, eurPerHour float64) error {
	floor, ok := FLOORS[category]
	if !ok {
		return fmt.Errorf("categorie inconnue: %s", category)
	}
	if eurPerHour < floor {
		return fmt.Errorf("sous le plancher anti-churn (%.2f EUR/h)", floor)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.current[category]
	p.current[category] = eurPerHour
	p.history = append(p.history, PriceChange{category, old, eurPerHour, time.Now()})
	p.addChurnAlertLocked(category, eurPerHour)
	p.store.SavePriceChange(category, eurPerHour)
	return nil
}

// addChurnAlertLocked : alerte si la baisse depasse le seuil sur 24 h.
func (p *Pricing) addChurnAlertLocked(category string, eurPerHour float64) {
	ref := p.priceAgoLocked(category, churnWindow)
	if ref > 0 && eurPerHour < ref*(1-churnDropThreshold) {
		msg := fmt.Sprintf(
			"churn: %s passe de %.2f a %.2f EUR/h (-%.0f%%)",
			category, ref, eurPerHour, (ref-eurPerHour)/ref*100)
		p.alerts = append(p.alerts, Alert{Type: "churn", Msg: msg, At: time.Now()})
	}
}

// SetCPUPrice (Sprint 25) applique un nouveau tarif CPU, sous reserve
// du plancher anti-churn CPU. Persiste avec le prefixe "cpu:".
func (p *Pricing) SetCPUPrice(category string, eurPerHour float64) error {
	floor, ok := CPU_FLOORS[category]
	if !ok {
		return fmt.Errorf("categorie inconnue: %s", category)
	}
	if eurPerHour < floor {
		return fmt.Errorf("sous le plancher anti-churn CPU (%.3f EUR/h)", floor)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	old := p.cpuCurrent[category]
	p.cpuCurrent[category] = eurPerHour
	p.store.SavePriceChange("cpu:"+category, eurPerHour)
	if old > 0 && eurPerHour < old*(1-churnDropThreshold) {
		msg := fmt.Sprintf(
			"churn: cpu %s passe de %.3f a %.3f EUR/h (-%.0f%%)",
			category, old, eurPerHour, (old-eurPerHour)/old*100)
		p.alerts = append(p.alerts, Alert{Type: "churn", Msg: msg, At: time.Now()})
	}
	return nil
}

// requireAdmin : cle simple via l'en-tete X-Admin-Key (MVP ;
// remplacee par une vraie IAM avant la beta).
func requireAdmin(r *http.Request) bool {
	key := os.Getenv("ADMIN_KEY")
	if key == "" {
		key = "dev-admin-key"
	}
	return r.Header.Get("X-Admin-Key") == key
}
