package main

// LAI-28 - Object store des assets clients (MVP).
// Les fichiers sont stockes sur le disque de l'orchestrateur
// (repertoire data/assets), avec metadonnees en base : sha256,
// taille, zone, organisation proprietaire. Les identifiants et
// checksums sont embarques dans les chunks pour la future
// verification cote noeud (LAI-43).

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func assetsDir() string {
	dir := os.Getenv("ASSETS_DIR")
	if dir == "" {
		dir = "data/assets"
	}
	os.MkdirAll(dir, 0o700)
	return dir
}

// POST /v1/assets?filename=X&zone=Y : televerse un fichier binaire
// (corps brut de la requete, limite 100 Mo). Content-addressed : un
// contenu identique renvoie le meme asset_id.
func v1UploadAsset(orgs *OrgStore, st *Store) http.HandlerFunc {
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
			http.Error(w, "invalid body", http.StatusBadRequest)
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
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		st.SaveAsset(&Asset{
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
func v1DownloadAsset(orgs *OrgStore, st *Store) http.HandlerFunc {
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
		asset := st.GetAsset(id)
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
