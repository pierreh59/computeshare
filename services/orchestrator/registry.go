package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Node represente un noeud enregistre avec sa derniere presence.
type Node struct {
	ID          string    `json:"id"`
	GPUModel    string    `json:"gpu_model"`
	GPUCategory string    `json:"gpu_category"`
	Zone        string    `json:"zone"`
	TrustScore         float64   `json:"trust_score"`
	Status             string    `json:"status"`
	MachineFingerprint string    `json:"machine_fingerprint,omitempty"`
	LastSeen    time.Time `json:"last_seen"`
	Online      bool      `json:"online"`
}

// Registry : map protegee par mutex, avec purge des noeuds silencieux.
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	store *Store
	fraud *Fraud
}

const heartbeatTimeout = 60 * time.Second

func NewRegistry(store *Store, fraud *Fraud) *Registry {
	r := &Registry{nodes: make(map[string]*Node), store: store, fraud: fraud}
	go r.reaperLoop()
	return r
}

// Register ajoute ou rafraichit un noeud (idempotent).
// Register ajoute ou rafraichit un noeud (idempotent).
// LAI-44 : une empreinte machine deja vue sous un autre node_id
// declenche une alerte Sybil et une penalite de confiance.
func (r *Registry) Register(id, gpuModel, gpuCategory, zone, fingerprint string) *Node {
	if zone == "" {
		zone = "local"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		n = &Node{ID: id, TrustScore: 100, Status: "active"}
		r.nodes[id] = n
	}
	n.GPUModel = gpuModel
	n.GPUCategory = gpuCategory
	n.Zone = zone
	n.LastSeen = time.Now()
	n.Online = true

	// Anti-Sybil : meme machine sous deux identites ?
	// Uniquement a la creation d'une NOUVELLE identite : une simple
	// reconnexion (meme node_id) ne doit jamais etre penalisee,
	// sinon un redemarrage du client effacerait la confiance.
	if !ok && fingerprint != "" {
		for _, other := range r.nodes {
			if other.ID != id && other.MachineFingerprint == fingerprint {
				r.fraud.CheckSybil(id, other.ID, fingerprint)
				n.TrustScore -= trustPenaltySybil
				if n.TrustScore < 0 {
					n.TrustScore = 0
				}
				break
			}
		}
	}
	n.MachineFingerprint = fingerprint
	r.store.SaveNode(n)
	return n
}

// Restore recharge un noeud connu de la BDD au demarrage (hors ligne
// jusqu'a ce qu'il se re-enregistre).
func (r *Registry) Restore(id, gpuModel, gpuCategory string, trust float64, status, zone, fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if zone == "" {
		zone = "local"
	}
	r.nodes[id] = &Node{
		ID:                 id,
		GPUModel:           gpuModel,
		GPUCategory:        gpuCategory,
		Zone:               zone,
		MachineFingerprint: fingerprint,
		TrustScore:         trust,
		Status:             status,
		Online:             false,
	}
}

// TrustOf retourne le trust score courant d'un noeud (100 si inconnu).
// LAI-44 : base des penalites et recompenses dynamiques.
func (r *Registry) TrustOf(id string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return 100
	}
	return n.TrustScore
}

// AdjustTrust applique un delta de confiance (bornes 0-100, LAI-44).
func (r *Registry) AdjustTrust(id string, delta float64) {
	r.mu.Lock()
	n, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return
	}
	n.TrustScore += delta
	if n.TrustScore < 0 {
		n.TrustScore = 0
	}
	if n.TrustScore > 100 {
		n.TrustScore = 100
	}
	r.store.SaveNode(n)
	r.mu.Unlock()
}

// Heartbeat met a jour la presence d'un noeud.
func (r *Registry) Heartbeat(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return false
	}
	n.LastSeen = time.Now()
	n.Online = true
	return true
}

// IsOnline indique si un noeud est actuellement en ligne.
func (r *Registry) IsOnline(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	return ok && n.Online
}

// OnlineNodes liste les noeuds consideres en ligne.
func (r *Registry) OnlineNodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Node
	for _, n := range r.nodes {
		if n.Online {
			out = append(out, *n)
		}
	}
	return out
}

// reaperLoop marque hors-ligne les noeuds sans heartbeat recent
// et persiste la transition.
func (r *Registry) reaperLoop() {
	for range time.Tick(10 * time.Second) {
		r.mu.Lock()
		for _, n := range r.nodes {
			if time.Since(n.LastSeen) > heartbeatTimeout && n.Online {
				n.Online = false
				r.store.SetNodeOnline(n.ID, false)
			}
		}
		r.mu.Unlock()
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
