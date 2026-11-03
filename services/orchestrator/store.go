package main

// Sprint 8 - Persistance PostgreSQL des etats runtime.
// Mode degrade : si DATABASE_URL est vide ou la BDD injoignable,
// l'orchestrateur continue en memoire (best effort, jamais de crash).
// Sprint 9 - Persiste aussi les organisations clientes (LAI-29).
// Sprint 10 - Persiste les assets clients (LAI-28) et les nouvelles
// colonnes des chunks (attempts, zone, assets) et noeuds (zone).

import (
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// Store ecrit l'etat dans PostgreSQL. db == nil => mode memoire.
type Store struct {
	db *sql.DB
}

func NewStore() *Store {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Printf("store: DATABASE_URL absent, mode memoire (rien n'est persiste)")
		return &Store{db: nil}
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		log.Printf("store: ouverture impossible: %v - mode memoire", err)
		return &Store{db: nil}
	}
	if err := db.Ping(); err != nil {
		log.Printf("store: BDD injoignable: %v - mode memoire", err)
		db.Close()
		return &Store{db: nil}
	}
	log.Printf("store: PostgreSQL connecte, persistance activee")
	return &Store{db: db}
}

func (s *Store) enabled() bool { return s != nil && s.db != nil }

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// SaveNode insere ou rafraichit un noeud (idempotent).
func (s *Store) SaveNode(n *Node) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_nodes
		(id, gpu_model, gpu_category, zone, machine_fingerprint,
		 trust_score, status, last_seen, online)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET
		  gpu_model=EXCLUDED.gpu_model,
		  gpu_category=EXCLUDED.gpu_category,
		  zone=EXCLUDED.zone,
		  machine_fingerprint=EXCLUDED.machine_fingerprint,
		  last_seen=EXCLUDED.last_seen,
		  online=EXCLUDED.online`,
		n.ID, n.GPUModel, n.GPUCategory, n.Zone, n.MachineFingerprint,
		n.TrustScore, n.Status, n.LastSeen, n.Online)
	if err != nil {
		log.Printf("store.SaveNode: %v", err)
	}
}

// SetNodeOnline marque un noeud en ligne ou hors ligne.
func (s *Store) SetNodeOnline(id string, online bool) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`UPDATE runtime_nodes SET online=$2, last_seen=now() WHERE id=$1`,
		id, online)
	if err != nil {
		log.Printf("store.SetNodeOnline: %v", err)
	}
}

// SaveChunk ecrit l'etat complet d'un chunk (insertion ou mise a jour).
func (s *Store) SaveChunk(ch *Chunk) {
	if !s.enabled() {
		return
	}
	args, _ := json.Marshal(ch.Args)
	assets, _ := json.Marshal(ch.AssetIDs)
	_, err := s.db.Exec(`INSERT INTO runtime_chunks
		(id, job_id, node_id, command, args, expected_output, timeout_secs,
		 status, result_sha256, price_eur_per_hour, payout_eur,
		 attempts, zone, assets, duration_secs, sampled, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16, now())
		ON CONFLICT (id) DO UPDATE SET
		  status=EXCLUDED.status,
		  result_sha256=EXCLUDED.result_sha256,
		  price_eur_per_hour=EXCLUDED.price_eur_per_hour,
		  payout_eur=EXCLUDED.payout_eur,
		  attempts=EXCLUDED.attempts,
		  node_id=EXCLUDED.node_id,
		  duration_secs=EXCLUDED.duration_secs,
		  sampled=EXCLUDED.sampled,
		  updated_at=now()`,
		ch.ID, ch.JobID, ch.NodeID, ch.Command, string(args), ch.ExpectedOutput,
		ch.TimeoutSecs, ch.Status, nullStr(ch.ResultSHA256),
		ch.PriceEurPerHour, ch.PayoutEur,
		ch.Attempts, nullStr(ch.Zone), string(assets),
		ch.DurationSecs, ch.Sampled)
	if err != nil {
		log.Printf("store.SaveChunk: %v", err)
	}
}

// SavePriceChange journalise un changement de tarif.
func (s *Store) SavePriceChange(category string, eurPerHour float64) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(
		`INSERT INTO runtime_price_history (category, eur_per_hour) VALUES ($1,$2)`,
		category, eurPerHour)
	if err != nil {
		log.Printf("store.SavePriceChange: %v", err)
	}
}

// SaveOrg insere ou rafraichit une organisation cliente (LAI-29).
func (s *Store) SaveOrg(org *Org, keyHash string) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_orgs (id, name, api_key_hash, created_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET
		  name=EXCLUDED.name,
		  api_key_hash=EXCLUDED.api_key_hash`,
		org.ID, org.Name, keyHash, org.CreatedAt)
	if err != nil {
		log.Printf("store.SaveOrg: %v", err)
	}
}

// Asset : fichier client stocke par l'orchestrateur (LAI-28).
type Asset struct {
	ID        string `json:"asset_id"`
	OrgID     string `json:"-"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Zone      string `json:"zone"`
}

// SaveAsset enregistre les metadonnees d'un asset (idempotent :
// un contenu identique renvoie le meme asset_id).
func (s *Store) SaveAsset(a *Asset) {
	if !s.enabled() {
		return
	}
	_, err := s.db.Exec(`INSERT INTO runtime_assets
		(id, org_id, filename, sha256, size_bytes, zone)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO NOTHING`,
		a.ID, a.OrgID, a.Filename, a.SHA256, a.SizeBytes, a.Zone)
	if err != nil {
		log.Printf("store.SaveAsset: %v", err)
	}
}

// GetAsset charge les metadonnees d'un asset (nil si inconnu).
func (s *Store) GetAsset(id string) *Asset {
	if !s.enabled() {
		return nil
	}
	row := s.db.QueryRow(`SELECT id, org_id, filename, sha256, size_bytes, zone
		FROM runtime_assets WHERE id=$1`, id)
	var a Asset
	err := row.Scan(&a.ID, &a.OrgID, &a.Filename, &a.SHA256, &a.SizeBytes, &a.Zone)
	if err != nil {
		return nil
	}
	return &a
}

// LoadAll recharge l'etat au demarrage : prix courants, noeuds connus,
// organisations clientes, chunks non termines.
func (s *Store) LoadAll(reg *Registry, pricing *Pricing, chunker *Chunker, orgs *OrgStore) {
	if !s.enabled() {
		return
	}
	// 1. Prix courants : derniere valeur par categorie.
	rows, err := s.db.Query(`SELECT DISTINCT ON (category)
		category, eur_per_hour
		FROM runtime_price_history
		ORDER BY category, changed_at DESC`)
	if err != nil {
		log.Printf("store.LoadAll(prices): %v", err)
	} else {
		for rows.Next() {
			var cat string
			var eur float64
			if rows.Scan(&cat, &eur) == nil {
				pricing.Restore(cat, eur)
			}
		}
		rows.Close()
	}

	// 2. Noeuds connus (hors ligne jusqu'a re-enregistrement).
	rows, err = s.db.Query(`SELECT id, gpu_model, gpu_category,
		trust_score, status, zone,
		COALESCE(machine_fingerprint, '') FROM runtime_nodes`)
	if err != nil {
		log.Printf("store.LoadAll(nodes): %v", err)
	} else {
		for rows.Next() {
			var id, model, cat, status, zone, fingerprint string
			var trust float64
			if rows.Scan(&id, &model, &cat, &trust, &status, &zone, &fingerprint) == nil {
				reg.Restore(id, model, cat, trust, status, zone, fingerprint)
			}
		}
		rows.Close()
	}

	// 3. Organisations clientes.
	rows, err = s.db.Query(`SELECT id, name, api_key_hash FROM runtime_orgs`)
	if err != nil {
		log.Printf("store.LoadAll(orgs): %v", err)
	} else {
		for rows.Next() {
			var id, name, keyHash string
			if rows.Scan(&id, &name, &keyHash) == nil {
				orgs.Restore(id, name, keyHash)
			}
		}
		rows.Close()
	}

	// 4. Chunks non termines (running -> queued, re-claimables).
	rows, err = s.db.Query(`SELECT id, job_id, node_id, command, args,
		expected_output, timeout_secs, price_eur_per_hour,
		attempts, zone, assets
		FROM runtime_chunks WHERE status IN ('queued','running')`)
	if err != nil {
		log.Printf("store.LoadAll(chunks): %v", err)
	} else {
		for rows.Next() {
			var id, jobID, nodeID, cmd, argsJSON, output string
			var timeout, price float64
			var attempts int
			var zone sql.NullString
			var assetsJSON string
			if rows.Scan(&id, &jobID, &nodeID, &cmd, &argsJSON, &output,
				&timeout, &price, &attempts, &zone, &assetsJSON) == nil {
				var args, assets []string
				if json.Unmarshal([]byte(argsJSON), &args) != nil {
					args = nil
				}
				if json.Unmarshal([]byte(assetsJSON), &assets) != nil {
					assets = nil
				}
				chunker.Restore(&Chunk{
					ID: id, JobID: jobID, NodeID: nodeID,
					Command: cmd, Args: args,
					ExpectedOutput:  output,
					TimeoutSecs:     uint64(timeout),
					Status:          "queued",
					PriceEurPerHour: price,
					Attempts:        attempts,
					Zone:            zone.String,
					AssetIDs:        assets,
				})
			}
		}
		rows.Close()
	}
	log.Printf("store: etat recharge depuis PostgreSQL (%s)",
		time.Now().Format("15:04:05"))
}
