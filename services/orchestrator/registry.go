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
	TrustScore  float64   `json:"trust_score"`
	Status      string    `json:"status"`
	LastSeen    time.Time `json:"last_seen"`
	Online      bool      `json:"online"`
}

// Registry : map protegee par mutex, avec purge des noeuds silencieux.
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	store *Store
}

const heartbeatTimeout = 60 * time.Second

func NewRegistry(store *Store) *Registry {
	r := &Registry{nodes: make(map[string]*Node), store: store}
	go r.reaperLoop()
	return r
}

// Register ajoute ou rafraichit un noeud (idempotent).
func (r *Registry) Register(id, gpuModel, gpuCategory string) *Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		n = &Node{ID: id, TrustScore: 100, Status: "active"}
		r.nodes[id] = n
	}
	n.GPUModel = gpuModel
	n.GPUCategory = gpuCategory
	n.LastSeen = time.Now()
	n.Online = true
	r.store.SaveNode(n)
	return n
}

// Restore recharge un noeud connu de la BDD au demarrage (hors ligne
// jusqu'a ce qu'il se re-enregistre).
func (r *Registry) Restore(id, gpuModel, gpuCategory string, trust float64, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = &Node{
		ID:          id,
		GPUModel:    gpuModel,
		GPUCategory: gpuCategory,
		TrustScore:  trust,
		Status:      status,
		Online:      false,
	}
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
