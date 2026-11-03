package main

// LAI-25 - Chunker : decoupe un job client en chunks assignes aux noeuds.
// LAI-51/52 - Le tarif est gele au moment de la livraison du chunk
// (jamais retroactif) et le paiement est calcule a la completion.

import (
"encoding/json"
"fmt"
"net/http"
"sync"
)

// Job soumis par un client B2B.
type Job struct {
ID         string   `json:"id"`
Command    string   `json:"command"`
Args       []string `json:"args"`
TaskCount  int      `json:"task_count"`
OutputName string   `json:"output_name"`
TimeoutSec uint64   `json:"timeout_secs"`
}

// Chunk : une unite de travail assignee a un seul noeud.
type Chunk struct {
ID             string   `json:"id"`
JobID          string   `json:"job_id"`
NodeID         string   `json:"node_id"`
Command        string   `json:"command"`
Args           []string `json:"args"`
ExpectedOutput string   `json:"expected_output"`
TimeoutSecs    uint64   `json:"timeout_secs"`
Status         string   `json:"status"`
ResultSHA256   string   `json:"result_sha256,omitempty"`
// LAI-51 : tarif gele a la livraison (EUR / GPU-heure).
PriceEurPerHour float64 `json:"price_eur_per_hour,omitempty"`
// Paiement calcule a la completion (EUR).
PayoutEur float64 `json:"payout_eur,omitempty"`
}

// Chunker : ordre de creation conserve pour une distribution stable.
type Chunker struct {
mu      sync.Mutex
reg     *Registry
pricing *Pricing
chunks  []*Chunk
seq     int
}

func NewChunker(r *Registry, p *Pricing) *Chunker {
return &Chunker{reg: r, pricing: p, chunks: make([]*Chunk, 0)}
}

// nodeCategory retrouve la categorie GPU d'un noeud inscrit.
func (c *Chunker) nodeCategory(nodeID string) string {
for _, n := range c.reg.OnlineNodes() {
if n.ID == nodeID {
return n.GPUCategory
}
}
return ""
}

// Submit decoupe le job en TaskCount chunks, distribues en round-robin
// sur les noeuds en ligne. Retourne nil si aucun noeud n'est dispo.
func (c *Chunker) Submit(job Job) []*Chunk {
nodes := c.reg.OnlineNodes()
if len(nodes) == 0 || job.TaskCount <= 0 {
return nil
}
c.mu.Lock()
defer c.mu.Unlock()
out := make([]*Chunk, 0, job.TaskCount)
for i := 0; i < job.TaskCount; i++ {
c.seq++
id := fmt.Sprintf("%s-%04d", job.ID, c.seq)
ch := &Chunk{
ID:             id,
JobID:          job.ID,
NodeID:         nodes[i%len(nodes)].ID,
Command:        job.Command,
Args:           job.Args,
ExpectedOutput: job.OutputName,
TimeoutSecs:    job.TimeoutSec,
Status:         "queued",
}
c.chunks = append(c.chunks, ch)
out = append(out, ch)
}
return out
}

// NextFor livre le prochain chunk en attente a un noeud, le passe a
// "running" et GELE son tarif au tarif courant de la categorie (ADR-006).
func (c *Chunker) NextFor(nodeID string) *Chunk {
c.mu.Lock()
defer c.mu.Unlock()
for _, ch := range c.chunks {
if ch.NodeID == nodeID && ch.Status == "queued" {
ch.Status = "running"
ch.PriceEurPerHour = c.pricing.CurrentPrice(c.nodeCategory(nodeID))
return ch
}
}
return nil
}

// Complete enregistre le resultat d'un chunk et calcule le paiement :
// tarif gele x duree reelle. Jamais retroactif (ADR-006).
func (c *Chunker) Complete(chunkID, outcome, sha string, durationSecs uint64) bool {
c.mu.Lock()
defer c.mu.Unlock()
for _, ch := range c.chunks {
if ch.ID == chunkID {
if outcome == "ok" {
ch.Status = "done"
ch.ResultSHA256 = sha
hours := float64(durationSecs) / 3600.0
ch.PayoutEur = ch.PriceEurPerHour * hours
} else {
ch.Status = "failed"
}
return true
}
}
return false
}

// JobStatus resume l'avancement d'un job (comptages par statut).
func (c *Chunker) JobStatus(jobID string) map[string]int {
c.mu.Lock()
defer c.mu.Unlock()
st := map[string]int{"queued": 0, "running": 0, "done": 0, "failed": 0}
for _, ch := range c.chunks {
if ch.JobID == jobID {
st[ch.Status]++
}
}
return st
}

// --- Endpoints HTTP ---

// POST /jobs : le client soumet un job, recoit ses chunks.
func submitJob(c *Chunker) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
var job Job
if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
http.Error(w, "invalid job", http.StatusBadRequest)
return
}
writeJSON(w, c.Submit(job))
}
}

// GET /chunks?node_id=X : le noeud reclame son prochain chunk.
func nextChunk(c *Chunker) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
writeJSON(w, c.NextFor(r.URL.Query().Get("node_id")))
}
}

// POST /chunk_result : le noeud signale {chunk_id, outcome, sha256, duration_secs}.
func completeChunk(c *Chunker) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
var res struct {
ChunkID      string `json:"chunk_id"`
Outcome      string `json:"outcome"`
SHA256       string `json:"sha256"`
DurationSecs uint64 `json:"duration_secs"`
}
if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
http.Error(w, "invalid result", http.StatusBadRequest)
return
}
ok := c.Complete(res.ChunkID, res.Outcome, res.SHA256, res.DurationSecs)
writeJSON(w, map[string]bool{"recorded": ok})
}
}
