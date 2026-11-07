package main

// LAI-25 - Chunker : decoupe un job client en chunks assignes aux noeuds.
// LAI-51/52 - Le tarif est gele au moment de la livraison du chunk
// (jamais retroactif) et le paiement est calcule a la completion.
// LAI-27 - Re-scheduling : un chunk dont le noeud disparait est
// redistribue a un autre noeud (preference meme zone), 3 tentatives max.
// Sprint 8 - Chaque transition d'etat est persistee dans PostgreSQL.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job soumis par un client B2B.
type Job struct {
	ID         string   `json:"id"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	TaskCount  int      `json:"task_count"`
	OutputName string   `json:"output_name"`
	TimeoutSec uint64   `json:"timeout_secs"`
	Zone       string   `json:"zone,omitempty"`
	AssetIDs   []string `json:"asset_ids,omitempty"`
	// Sprint 18 (v2) : runtime d'execution. "" ou "allowlist" = mode
	// v1 (commande parmi l'allowlist du sandbox desktop) ; "container"
	// = le noeud tire l'image et l'execute dans WSL2 (adresse de
	// registre Docker passee par Image).
	Runtime string `json:"runtime,omitempty"`
	Image   string `json:"image,omitempty"`
	// Sprint 25 : "gpu": true => le job DOIT tourner sur un noeud
	// "GPU pret" (probe docker --gpus reussie) et le conteneur est
	// lance avec --gpus all. Le tarif gele est alors le tarif GPU,
	// sinon le tarif CPU (beaucoup moins cher).
	Gpu bool `json:"gpu,omitempty"`
}

// Chunk : une unite de travail assignee a un seul noeud.
type Chunk struct {
	ID      string `json:"id"`
	JobID   string `json:"job_id"`
	NodeID  string `json:"node_id"`
	Command string `json:"command"`
	// Organisation cliente B2B proprietaire du chunk (vide pour les
	// soumissions internes). Utilisee par le billing (LAI-32).
	OrgID          string   `json:"-"`
	Args           []string `json:"args"`
	ExpectedOutput string   `json:"expected_output"`
	TimeoutSecs    uint64   `json:"timeout_secs"`
	Status         string   `json:"status"`
	ResultSHA256   string   `json:"result_sha256,omitempty"`
	Attempts       int      `json:"attempts,omitempty"`
	Zone           string   `json:"zone,omitempty"`
	AssetIDs       []string `json:"asset_ids,omitempty"`
	// Sprint 22 : heure de derniere mise en route (memoire seulement).
	// Le re-scheduler ne redistribue plus un chunk tant que son timeout
	// n'est pas expire, meme si le noeud parait hors ligne (vu en vrai :
	// heartbeat bloque pendant un job conteneur de 5 min).
	StartedAt time.Time `json:"-"`
	// Sprint 18 (v2) : heritage du runtime conteneur du job.
	Runtime string `json:"runtime,omitempty"`
	Image   string `json:"image,omitempty"`
	// Sprint 25 : heritage du flag GPU du job (routage + --gpus all
	// cote desktop + choix du tarif CPU/GPU a la livraison).
	Gpu bool `json:"gpu,omitempty"`
	// Sprint 26b : detail d'erreur rapporte par le noeud (memoire
	// seulement, affiche dans le journal du dashboard admin).
	Detail string `json:"-"`
	// LAI-51 : tarif gele a la livraison (EUR / GPU-heure).
	PriceEurPerHour float64 `json:"price_eur_per_hour,omitempty"`
	// Paiement calcule a la completion (EUR).
	PayoutEur float64 `json:"payout_eur,omitempty"`
	// LAI-43/44 : duree reelle declaree et flag d'echantillonnage.
	DurationSecs uint64 `json:"duration_secs,omitempty"`
	Sampled      bool   `json:"sampled,omitempty"`
}

// Chunker : ordre de creation conserve pour une distribution stable.
type Chunker struct {
	mu      sync.Mutex
	reg     *Registry
	pricing *Pricing
	store   *Store
	fraud   *Fraud
	wallet  *Wallet
	billing *Billing
	chunks  []*Chunk
	seq     int
}

const maxChunkAttempts = 3

func NewChunker(r *Registry, p *Pricing, s *Store, f *Fraud, w *Wallet, b *Billing) *Chunker {
	c := &Chunker{reg: r, pricing: p, store: s, fraud: f, wallet: w, billing: b, chunks: make([]*Chunk, 0)}
	go c.rescheduleLoop()
	return c
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
// JobExists indique si un job a deja ete soumis (LAI-55 :
// idempotence des soumissions, protection contre les doubles
// facturations accidentelles lors des retries reseau).
func (c *Chunker) JobExists(jobID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.chunks {
		if ch.JobID == jobID {
			return true
		}
	}
	return false
}

func (c *Chunker) Submit(job Job, orgID string) []*Chunk {
	nodes := c.reg.OnlineNodes()
	if job.TaskCount <= 0 {
		return nil
	}
	// Sprint 18 (v2) : un job conteneur ne part QUE vers les noeuds
	// qui ont annonce WSL2Ready au register.
	if job.Runtime == "container" {
		ready := make([]Node, 0, len(nodes))
		for _, n := range nodes {
			if n.WSL2Ready {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			return nil
		}
		nodes = ready
	}
	// Sprint 25 : un job GPU ne part QUE vers les noeuds qui ont
	// annonce gpu_ready (probe `docker run --gpus all` reussie).
	if job.Gpu {
		ready := make([]Node, 0, len(nodes))
		for _, n := range nodes {
			if n.GpuReady {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			return nil
		}
		nodes = ready
	}
	if len(nodes) == 0 {
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
			OrgID:          orgID,
			Command:        job.Command,
			Args:           job.Args,
			ExpectedOutput: job.OutputName,
			TimeoutSecs:    job.TimeoutSec,
			Status:         "queued",
			Zone:           job.Zone,
			AssetIDs:       job.AssetIDs,
			Runtime:        job.Runtime,
			Image:          job.Image,
			Gpu:            job.Gpu,
		}
		c.chunks = append(c.chunks, ch)
		out = append(out, ch)
		c.store.SaveChunk(ch)
	}
	return out
}

// NextFor livre le prochain chunk en attente a un noeud, le passe a
// "running" et GELE son tarif au tarif courant de la categorie (ADR-006).
// Sprint 25 : le tarif gele depend du job - tarif GPU si le job demande
// le GPU, tarif CPU (moins cher) sinon. Jamais retroactif.
func (c *Chunker) NextFor(nodeID string) *Chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.chunks {
		if ch.NodeID == nodeID && ch.Status == "queued" {
			ch.Status = "running"
			ch.Attempts++
			ch.StartedAt = time.Now()
			if ch.Gpu {
				ch.PriceEurPerHour = c.pricing.CurrentPrice(c.nodeCategory(nodeID))
			} else {
				ch.PriceEurPerHour = c.pricing.CurrentCPUPrice(c.nodeCategory(nodeID))
			}
			c.store.SaveChunk(ch)
			return ch
		}
	}
	return nil
}

// Complete enregistre le resultat d'un chunk et calcule le paiement :
// tarif gele x duree reelle. Jamais retroactif (ADR-006).
// Sprint 22 : idempotent (un chunk deja termine ignore tout nouveau
// rapport, pas de double paiement) et le paiement va au noeud qui
// RAPPORTE (nodeID), pas au noeud theoretiquement assigne (en cas de
// re-attribution pendant l'execution, l'ancien noeud doit etre paye).
func (c *Chunker) Complete(chunkID, nodeID, outcome, sha, detail string, durationSecs uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.chunks {
		if ch.ID == chunkID {
			// Sprint 22 : rapport en double (re-attribution, doublon
			// de chunk_result) -> ignore, pas de second versement.
			if ch.Status == "done" || ch.Status == "failed" || ch.Status == "canceled" {
				return false
			}
			if nodeID == "" {
				nodeID = ch.NodeID
			}
			ch.DurationSecs = durationSecs
			if outcome == "ok" {
				ch.Status = "done"
				ch.ResultSHA256 = sha
				hours := float64(durationSecs) / 3600.0
				ch.PayoutEur = ch.PriceEurPerHour * hours
			} else {
				ch.Status = "failed"
			}
			// Sprint 22 : le rapport vient du vrai executeur.
			ch.NodeID = nodeID
			// Sprint 17 : journal temps reel du dashboard admin.
			if outcome == "ok" {
				dashLog("chunk_done", fmt.Sprintf("%s termine par %s (%.0f s, verse %.6f EUR)",
					ch.ID, ch.NodeID, float64(durationSecs), ch.PayoutEur))
			} else {
				// Sprint 26b : le detail du noeud complete le
				// verdict (motif Docker, fichier manquant, etc.).
				ch.Detail = detail
				msg := fmt.Sprintf("%s ECHEC sur %s : %s", ch.ID, ch.NodeID, outcome)
				if detail != "" {
					msg += " - " + detail
				}
				dashLog("chunk_failed", msg)
			}
			// LAI-43/44 : sampling et detection d'execution ralentie
			// (logique detaillee dans fraud.go).
			c.fraudCheck(ch, outcome)
			// LAI-34 : le gain est ecrit au grand livre du vendeur.
			if outcome == "ok" {
				c.wallet.CreditPayout(ch.NodeID, ch.PayoutEur, ch.ID)
			}
			// LAI-32 : le client B2B est facture a la GPU-heure
			// reelle (cout = reversement x marge, TVA journalisee).
			if outcome == "ok" {
				c.billing.ChargeChunk(ch.OrgID, ch.PayoutEur,
					float64(durationSecs)/3600.0, ch.ID)
			}
			c.store.SaveChunk(ch)
			return true
		}
	}
	return false
}

// rescheduleLoop (LAI-27) : toutes les 10 s, les chunks "running"
// dont le noeud a disparu sont redistribues. Apres 3 tentatives,
// le chunk passe en echec definitif.
// Sprint 22 : on attend aussi l'expiration du timeout du chunk avant
// de redistribuer (un job conteneur de 5 min avec le heartbeat bloque
// etait redistribue au milieu de son execution).
func (c *Chunker) rescheduleLoop() {
	for range time.Tick(10 * time.Second) {
		c.mu.Lock()
		online := c.reg.OnlineNodes()
		for _, ch := range c.chunks {
			if ch.Status != "running" || c.reg.IsOnline(ch.NodeID) {
				continue
			}
			// Sprint 22 : grace period = timeout du chunk (300 s min).
			grace := ch.TimeoutSecs
			if grace < 300 {
				grace = 300
			}
			if time.Since(ch.StartedAt) < time.Duration(grace)*time.Second {
				continue
			}
			if ch.Attempts >= maxChunkAttempts {
				ch.Status = "failed"
				c.store.SaveChunk(ch)
				continue
			}
			if next := pickNode(online, ch); next != "" {
				ch.NodeID = next
				ch.Status = "queued"
				c.store.SaveChunk(ch)
			}
			// Aucun noeud dispo : on retente au prochain tick.
		}
		c.mu.Unlock()
	}
}

// pickNode choisit un nouveau noeud pour un chunk, en privilegiant
// la meme zone (data-locality, LAI-28). Sprint 18 : un chunk conteneur
// n'est jamais re-confie a un noeud sans WSL2. Sprint 25 : un chunk
// GPU n'est jamais re-confie a un noeud sans gpu_ready.
func pickNode(online []Node, ch *Chunk) string {
	for _, n := range online {
		if ch.Runtime == "container" && !n.WSL2Ready {
			continue
		}
		if ch.Gpu && !n.GpuReady {
			continue
		}
		if n.ID != ch.NodeID && n.Zone == ch.Zone {
			return n.ID
		}
	}
	for _, n := range online {
		if ch.Runtime == "container" && !n.WSL2Ready {
			continue
		}
		if ch.Gpu && !n.GpuReady {
			continue
		}
		if n.ID != ch.NodeID {
			return n.ID
		}
	}
	return ""
}

// CancelJob annule les chunks pas encore demarres.
func (c *Chunker) CancelJob(jobID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, ch := range c.chunks {
		if ch.JobID == jobID && ch.Status == "queued" {
			ch.Status = "canceled"
			c.store.SaveChunk(ch)
			n++
		}
	}
	return n
}

// Sprint 26 : retrouve un chunk en memoire, quel que soit son statut
// (sert a l'upload des fichiers de resultat).
func (c *Chunker) ChunkByID(chunkID string) *Chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.chunks {
		if ch.ID == chunkID {
			return ch
		}
	}
	return nil
}

// Results liste les chunks termines d'un job avec leurs checksums.
func (c *Chunker) Results(jobID string) []Chunk {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Chunk
	for _, ch := range c.chunks {
		if ch.JobID == jobID && ch.Status == "done" {
			out = append(out, *ch)
		}
	}
	return out
}

// Restore recharge un chunk non termine au demarrage : les chunks
// "running" au moment de l'arret redeviennent "queued" (re-claimables).
func (c *Chunker) Restore(ch *Chunk) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := seqOf(ch.ID); n > c.seq {
		c.seq = n
	}
	ch.Status = "queued"
	c.chunks = append(c.chunks, ch)
}

// seqOf extrait le numero de sequence d'un identifiant "job-0007".
func seqOf(id string) int {
	i := strings.LastIndex(id, "-")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(id[i+1:])
	return n
}

// JobStatus resume l'avancement d'un job (comptages par statut).
func (c *Chunker) JobStatus(jobID string) map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := map[string]int{
		"queued": 0, "running": 0, "done": 0, "failed": 0, "canceled": 0,
	}
	for _, ch := range c.chunks {
		if ch.JobID == jobID {
			st[ch.Status]++
		}
	}
	return st
}

// Sprint 23 : OrgJobs resume l'ensemble des jobs d'une organisation.
// Retourne pour chaque job : id, nombre de taches, comptages par statut.
// L'ordre est celui de la soumission (les chunks sont ajoutes sequentiellement).
func (c *Chunker) OrgJobs(orgID string, limit int) []map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	order := []string{}
	idx := map[string]int{}
	for _, ch := range c.chunks {
		if ch.OrgID != orgID {
			continue
		}
		i, ok := idx[ch.JobID]
		if !ok {
			i = len(order)
			idx[ch.JobID] = i
			order = append(order, ch.JobID)
		}
	}
	out := make([]map[string]interface{}, 0, len(order))
	for _, jobID := range order {
		st := map[string]int{
			"queued": 0, "running": 0, "done": 0, "failed": 0, "canceled": 0,
		}
		total := 0
		for _, ch := range c.chunks {
			if ch.JobID == jobID && ch.OrgID == orgID {
				st[ch.Status]++
				total++
			}
		}
		out = append(out, map[string]interface{}{
			"job_id":     jobID,
			"task_count": total,
			"chunks":     st,
		})
	}
	if limit > 0 && len(out) > limit {
		// Les plus recents a la fin : on garde la fin de la liste.
		out = out[len(out)-limit:]
	}
	// Sprint 24 : l'historique persiste (runtime_chunks) complete la
	// liste. Les jobs vivants en memoire prennent le pas sur la base,
	// les jobs termines d'avant le dernier redemarrage restent visibles.
	live := map[string]bool{}
	for _, j := range out {
		live[j["job_id"].(string)] = true
	}
	dbLimit := limit
	if dbLimit <= 0 {
		dbLimit = 100
	}
	for _, r := range c.store.LoadOrgJobs(orgID, dbLimit) {
		if live[r.JobID] {
			continue
		}
		out = append(out, map[string]interface{}{
			"job_id":     r.JobID,
			"task_count": r.TaskCount,
			"chunks":     r.Statuses,
			"updated_at": r.UpdatedAt,
		})
	}
	return out
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
		// LAI-55 : idempotence egalement sur la soumission interne.
		if c.JobExists(job.ID) {
			http.Error(w, "job id already exists", http.StatusConflict)
			return
		}
		writeJSON(w, c.Submit(job, ""))
	}
}

// GET /chunks?node_id=X : le noeud reclame son prochain chunk.
func nextChunk(c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.NextFor(r.URL.Query().Get("node_id")))
	}
}

// POST /chunk_result : le noeud signale {chunk_id, outcome, sha256,
// duration_secs, node_id} (node_id depuis le sprint 22 : le paiement
// va au noeud qui rapporte, pas a l'assigne theorique).
func completeChunk(c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var res struct {
			ChunkID string `json:"chunk_id"`
			NodeID  string `json:"node_id"`
			Outcome string `json:"outcome"`
			SHA256  string `json:"sha256"`
			// Sprint 26b : detail d'erreur optionnel (tronque).
			Detail       string `json:"detail"`
			DurationSecs uint64 `json:"duration_secs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&res); err != nil {
			http.Error(w, "invalid result", http.StatusBadRequest)
			return
		}
		if len(res.Detail) > 500 {
			res.Detail = res.Detail[:500]
		}
		ok := c.Complete(res.ChunkID, res.NodeID, res.Outcome, res.SHA256, res.Detail, res.DurationSecs)
		writeJSON(w, map[string]bool{"recorded": ok})
	}
}
