package main

// LAI-43 - Proof-of-execution (MVP) : sampling aleatoire des chunks
// termines (10 %), conserves en base pour audit, en plus des checksums
// SHA-256 deja remontes par les noeuds.
// LAI-44 - Anti-fraude MVP : detection d'execution ralentie par
// comparaison a la mediane des pairs du meme job, penalites de trust
// score, alertes anti-Sybil (empreinte machine dupliquee).

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"
)

const (
	sampleRate       = 0.10 // 10 % des chunks termines sont echantillonnes
	slowFactor       = 3.0  // 3x la mediane des pairs = suspect
	slowMinSeconds   = 30   // ignorer les executions trop courtes
	trustPenaltySlow = 10
	trustPenaltyFail = 5
	trustRewardOk    = 1
	trustPenaltySybil = 50
)

// FraudAlert : evenement anti-fraude signale a l'admin.
type FraudAlert struct {
	Type   string    `json:"type"`
	NodeID string    `json:"node_id"`
	Msg    string    `json:"msg"`
	At     time.Time `json:"at"`
}

type Fraud struct {
	mu     sync.Mutex
	alerts []FraudAlert
}

func NewFraud() *Fraud { return &Fraud{} }

func (f *Fraud) alert(alertType, nodeID, msg string) {
	f.mu.Lock()
	f.alerts = append(f.alerts, FraudAlert{
		Type: alertType, NodeID: nodeID, Msg: msg, At: time.Now(),
	})
	f.mu.Unlock()
	// Sprint 17 : visible en direct dans le dashboard admin.
	dashLog("fraud", "["+alertType+"] "+msg)
}

// Alerts copie des alertes, plus recentes en premier.
func (f *Fraud) Alerts() []FraudAlert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FraudAlert, 0, len(f.alerts))
	for i := len(f.alerts) - 1; i >= 0; i-- {
		out = append(out, f.alerts[i])
	}
	return out
}

// CheckSybil signale deux noeuds revendiquant la meme machine.
// Appele depuis Registry.Register (mutex registry deja pris cote appelant
// via AdjustTrust, ici on ne touche pas au mutex registry).
func (f *Fraud) CheckSybil(newNodeID, existingNodeID, fingerprint string) {
	msg := fmt.Sprintf("empreinte machine dupliquee (%s) : %s reutilise celle de %s",
		fingerprint, newNodeID, existingNodeID)
	f.alert("sybil", newNodeID, msg)
}

// fraudCheck est appele par Chunker.Complete (mutex chunker deja pris).
// Il gere le sampling (LAI-43) et la detection de lenteur (LAI-44).
func (c *Chunker) fraudCheck(ch *Chunk, outcome string) {
	if outcome != "ok" {
		// Echec d'execution : legere penalite de confiance.
		c.reg.AdjustTrust(ch.NodeID, -trustPenaltyFail)
		return
	}

	// Reussite : recompense de confiance, plafonnee a 100.
	if c.reg.TrustOf(ch.NodeID) < 100 {
		c.reg.AdjustTrust(ch.NodeID, trustRewardOk)
	}

	// LAI-43 : sampling aleatoire pour audit futur.
	if rand.Float64() < sampleRate {
		ch.Sampled = true
	}

	// LAI-44 : execution ralentie ? Mediane des pairs du meme job.
	var others []float64
	for _, o := range c.chunks {
		if o.JobID == ch.JobID && o.ID != ch.ID &&
			o.Status == "done" && o.DurationSecs > 0 {
			others = append(others, float64(o.DurationSecs))
		}
	}
	if len(others) >= 2 && ch.DurationSecs > slowMinSeconds {
		sort.Float64s(others)
		median := others[len(others)/2]
		if float64(ch.DurationSecs) > median*slowFactor {
			msg := fmt.Sprintf(
				"execution ralentie : %s (%d s vs mediane pairs %.0f s)",
				ch.NodeID, ch.DurationSecs, median)
			c.fraud.alert("slow_execution", ch.NodeID, msg)
			c.reg.AdjustTrust(ch.NodeID, -trustPenaltySlow)
		}
	}
}
