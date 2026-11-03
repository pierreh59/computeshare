package main

import (
    "encoding/json"
    "net/http"
    "sync"
    "time"
)

type Node struct {
    ID          string    `json:"id"`
    GPUModel    string    `json:"gpu_model"`
    GPUCategory string    `json:"gpu_category"`
    TrustScore  float64   `json:"trust_score"`
    Status      string    `json:"status"`
    LastSeen    time.Time `json:"last_seen"`
    Online      bool      `json:"online"`
}

type Registry struct {
    mu    sync.RWMutex
    nodes map[string]*Node
}

const heartbeatTimeout = 60 * time.Second

func NewRegistry() *Registry {
    r := &Registry{nodes: make(map[string]*Node)}
    go r.reaperLoop()
    return r
}

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
    return n
}

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

func (r *Registry) reaperLoop() {
    for range time.Tick(10 * time.Second) {
        r.mu.Lock()
        for _, n := range r.nodes {
            if time.Since(n.LastSeen) > heartbeatTimeout {
                n.Online = false
            }
        }
        r.mu.Unlock()
    }
}

func writeJSON(w http.ResponseWriter, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(v)
}
