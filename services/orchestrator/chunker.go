package main

// LAI-25 - Chunker : decoupe un job client en chunks assignes aux noeuds.

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
TaskCount  int      `json:"task_count"` // nb de chunks voulus
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
Status         string   `json:"status"` // queued | running | done | failed
ResultSHA256   string   `json:"result_sha256,omitempty"`
}

// Chunker : ordre de creation conserve pour une distribution stable.
type Chunker struct {
mu     sync.Mutex
reg    *Registry
chunks []*Chunk
seq    int
}

func NewChunker(r *Registry) *Chunker {
return &Chunker{reg: r, chunks: make([]*Chunk, 0)}
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

// NextFor renvoie le prochain chunk en attente pour un noeud
// et le passe a "running" (livre une seule fois).
func (c *Chunker) NextFor(nodeID string) *Chunk {
c.mu.Lock()
defer c.mu.Unlock()
for _, ch := range c.chunks {
if ch.NodeID == nodeID && ch.Status == "queued" {
ch.Status = "running"
return ch
}
}
return nil
}

// Complete enregistre le resultat d'un chunk (checksum SHA-256).
func (c *Chunker) Complete(chunkID, outcome, sha string) bool {
c.mu.Lock()
defer c.mu.Unlock()
for _, ch := range c.chunks {
if ch.ID == chunkID {
if outcome == "ok" {
ch.Status = "done"
ch.ResultSHA256 = sha
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
ch := c.NextFor(r.URL.Query().Get("node_id"))
writeJSON(w, ch)
}
}

// POST /chunk_result : le noeud signale {chunk_id, outcome, sha256}.
func completeChunk(c *Chunker) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
var res struct {
ChunkID string `json:"chunk_id"`
Outcome string `json:"outcome"`
SHA256  string `json:"sha256"`
}
if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
http.Error(w, "invalid result", http.StatusBadRequest)
return
}
writeJSON(w, map[string]bool{"recorded": c.Complete(res.ChunkID, res.Outcome, res.SHA256)})
}
}
