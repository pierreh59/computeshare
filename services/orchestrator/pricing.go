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
	"sync"
	"time"
)

// Planchers de reversement en EUR / GPU-heure (anti-churn).
var FLOORS = map[string]float64{
	"consumer": 0.12,
	"prosumer": 0.20,
	"high_end": 0.30,
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
	mu      sync.RWMutex
	current map[string]float64
	history []PriceChange
	alerts  []Alert
	store   *Store
}

func NewPricing(store *Store) *Pricing {
	return &Pricing{
		current: map[string]float64{
			"consumer": 0.15,
			"prosumer": 0.25,
			"high_end": 0.35,
		},
		store: store,
	}
}

// CurrentPrice renvoie le tarif courant d'une categorie (0 si inconnue).
func (p *Pricing) CurrentPrice(category string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current[category]
}

// Restore recharge un tarif depuis la BDD au demarrage.
func (p *Pricing) Restore(category string, eurPerHour float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
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

// requireAdmin : cle simple via l'en-tete X-Admin-Key (MVP ;
// remplacee par une vraie IAM avant la beta).
func requireAdmin(r *http.Request) bool {
	key := os.Getenv("ADMIN_KEY")
	if key == "" {
		key = "dev-admin-key"
	}
	return r.Header.Get("X-Admin-Key") == key
}
