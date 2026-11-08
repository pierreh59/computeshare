package main

// LAI-28 - Object store des assets clients.
// Sprint 27 : les assets deviennent de vraies ENTREES de job. Le
// client televerse un fichier (/v1/assets), le reference dans
// asset_ids a la soumission ; le manifeste (nom de fichier, sha256,
// taille) est embarque dans le chunk livre au noeud (/chunks), qui
// telecharge chaque fichier (sha256 verifie) avant d'executer le
// conteneur et le pose dans le dossier monte sur /work.
// Les fichiers sont stockes sur le disque de l'orchestrateur
// (ASSETS_DIR), avec metadonnees en base : sha256, taille, zone,
// organisation proprietaire (checksums embarques pour la
// verification cote noeud, LAI-43).

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func assetsDir() string {
	dir := os.Getenv("ASSETS_DIR")
	if dir == "" {
		dir = "data/assets"
	}
	// Sprint 26 (lecon retenue) : un echec de creation avale se paie
	// en debug nocturne ; on le rend visible dans les logs.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("assets: creation de %s impossible : %v", dir, err)
	}
	return dir
}

// AssetRef : manifeste d'un asset d'entree embarque dans le chunk
// livre au noeud. Le noeud verifie le sha256 apres telechargement.
type AssetRef struct {
	AssetID   string `json:"asset_id"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// AssetStore : metadonnees des assets en memoire (meme philosophie
// que OrgStore), persistees dans Postgres si disponible. Get
// fonctionne donc meme sans base (tests, mode memoire), Save
// profite de la base en production. Au demarrage, les metadonnees
// existantes sont rechargees (les fichiers, eux, sont deja sur le
// volume ASSETS_DIR).
type AssetStore struct {
	mu    sync.Mutex
	byID  map[string]*Asset
	store *Store
}

func NewAssetStore(st *Store) *AssetStore {
	as := &AssetStore{byID: map[string]*Asset{}, store: st}
	for _, a := range st.LoadAssets() {
		as.byID[a.ID] = a
	}
	return as
}

func (as *AssetStore) Save(a *Asset) {
	as.mu.Lock()
	defer as.mu.Unlock()
	cp := *a
	as.byID[a.ID] = &cp
	as.store.SaveAsset(a)
}

func (as *AssetStore) Get(id string) *Asset {
	as.mu.Lock()
	defer as.mu.Unlock()
	if a, ok := as.byID[id]; ok {
		cp := *a
		return &cp
	}
	return nil
}

// Manifest construit la liste des AssetRef d'un chunk, dans l'ordre
// des asset_ids. Un id inconnu est saute : le noeud ne verra pas le
// fichier dans son manifeste et ne pourra pas le reclamer.
func (as *AssetStore) Manifest(ids []string) []AssetRef {
	as.mu.Lock()
	defer as.mu.Unlock()
	out := make([]AssetRef, 0, len(ids))
	for _, id := range ids {
		a, ok := as.byID[id]
		if !ok {
			continue
		}
		out = append(out, AssetRef{
			AssetID:   a.ID,
			Filename:  a.Filename,
			SHA256:    a.SHA256,
			SizeBytes: a.SizeBytes,
		})
	}
	return out
}

// deliverChunk livre le prochain chunk d'un noeud et l'enrichit du
// manifeste de ses assets d'entree (sprint 27).
func deliverChunk(c *Chunker, assets *AssetStore, nodeID string) *Chunk {
	ch := c.NextFor(nodeID)
	if ch != nil && len(ch.AssetIDs) > 0 {
		ch.Assets = assets.Manifest(ch.AssetIDs)
	}
	return ch
}

// nextChunkWithAssets : route /chunks enrichie (sprint 27).
func nextChunkWithAssets(c *Chunker, assets *AssetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, deliverChunk(c, assets, r.URL.Query().Get("node_id")))
	}
}

// POST /v1/assets?filename=X&zone=Y : televerse un fichier binaire
// (corps brut de la requete, limite 100 Mo). Content-addressed : un
// contenu identique renvoie le meme asset_id.
func v1UploadAsset(orgs *OrgStore, assets *AssetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		buf, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
		if err != nil || len(buf) == 0 {
			// Sprint 27b : message explicite (vu en vrai : un fichier
			// vide 0 octet televerse depuis la console affichait un
			// "invalid body" incomprehensible pour le client).
			http.Error(w, "fichier vide ou illisible", http.StatusBadRequest)
			return
		}
		sum := sha256.Sum256(buf)
		sha := hex.EncodeToString(sum[:])
		id := "asset-" + sha[:12]

		name := r.URL.Query().Get("filename")
		if name == "" {
			name = id
		}
		zone := r.URL.Query().Get("zone")
		if zone == "" {
			zone = "local"
		}

		path := filepath.Join(assetsDir(), id)
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			// Sprint 26 (lecon retenue) : le detail remonte au client
			// ET aux logs serveur, aucune erreur muette.
			log.Printf("assets: ecriture de %s impossible : %v", path, err)
			http.Error(w, "storage error : "+err.Error(), http.StatusInternalServerError)
			return
		}
		assets.Save(&Asset{
			ID: id, OrgID: org.ID, Filename: name,
			SHA256: sha, SizeBytes: int64(len(buf)), Zone: zone,
		})
		writeJSON(w, map[string]interface{}{
			"asset_id":   id,
			"sha256":     sha,
			"size_bytes": len(buf),
			"zone":       zone,
		})
	}
}

// GET /v1/assets/{id}/download : renvoie le fichier, uniquement a
// son organisation proprietaire.
func v1DownloadAsset(orgs *OrgStore, assets *AssetStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := strings.TrimSuffix(
			strings.TrimPrefix(r.URL.Path, "/v1/assets/"), "/download")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		asset := assets.Get(id)
		if asset == nil || asset.OrgID != org.ID {
			// 404 sans distinction : on ne revele pas l'existence
			// d'un asset appartenant a une autre organisation.
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(assetsDir(), id)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Sha256", asset.SHA256)
		http.ServeFile(w, r, path)
	}
}

// GET /v1/assets/{id}/node-download?node_id=X&chunk_id=Y : le noeud
// qui execute un chunk telecharge un asset d'entree reference par CE
// chunk (sprint 27). Authentification par jeton de noeud ; le chunk
// doit etre "running" sur CE noeud et referencer l'asset. Aucune
// fuite possible : un noeud ne peut obtenir que les fichiers des
// chunks qu'on lui a confies.
func v1NodeDownloadAsset(assets *AssetStore, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(
			strings.TrimPrefix(r.URL.Path, "/v1/assets/"), "/node-download")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		nodeID := r.URL.Query().Get("node_id")
		chunkID := r.URL.Query().Get("chunk_id")
		ch := c.ChunkByID(chunkID)
		if ch == nil || nodeID == "" || ch.NodeID != nodeID || ch.Status != "running" {
			http.NotFound(w, r)
			return
		}
		referenced := false
		for _, a := range ch.AssetIDs {
			if a == id {
				referenced = true
				break
			}
		}
		if !referenced {
			http.NotFound(w, r)
			return
		}
		asset := assets.Get(id)
		if asset == nil {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(assetsDir(), id)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Sha256", asset.SHA256)
		http.ServeFile(w, r, path)
	}
}
