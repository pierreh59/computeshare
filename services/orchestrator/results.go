package main

// Sprint 26 - Rapatriement des fichiers de resultat.
// Jusqu'ici le client ne recevait que l'empreinte SHA-256 de son
// fichier, jamais le fichier. Desormais :
//   - le noeud, apres avoir rapporte un chunk reussi, POUSSE le
//     fichier de sortie vers POST /results (corps = octets bruts,
//     en-tete X-Chunk-Id) ;
//   - le client le recupere via GET /v1/results/<chunk_id> (cle API) ;
//   - purge automatique des fichiers plus vieux que 30 jours ;
//   - garde-fous : taille max 5 Go par fichier, refus si l'espace
//     disque libre du serveur descend sous le plancher.
//
// Stockage : disque du VPS (decision Pierre, 7 nov.), bascule S3 OVH
// prevue si le volume devient le facteur limitant.

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	maxResultBytes  = int64(5 << 30) // 5 Go par fichier (decision Pierre)
	resultFreeFloor = int64(5 << 30) // garder 5 Go de libre apres ecriture
	resultRetention = 30 * 24 * time.Hour
)

// resultsDir : repertoire de stockage (RESULTS_DIR, defaut relatif).
func resultsDir() string {
	d := os.Getenv("RESULTS_DIR")
	if d == "" {
		d = "results-data"
	}
	os.MkdirAll(d, 0o750)
	return d
}

func resultPath(chunkID string) string {
	id := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, chunkID)
	if len(id) > 128 {
		id = id[:128]
	}
	return filepath.Join(resultsDir(), id)
}

// freeBytes : espace libre du systeme de fichiers du repertoire.
func freeBytes(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 1 << 62 // erreur : on ne bloque pas sur une statdefaillante
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// POST /results : upload d'un fichier de resultat par un noeud.
// Corps = octets bruts du fichier, X-Chunk-Id = chunk termine.
func uploadResult(c *Chunker, store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chunkID := r.Header.Get("X-Chunk-Id")
		if chunkID == "" {
			http.Error(w, "X-Chunk-Id manquant", http.StatusBadRequest)
			return
		}
		// Le chunk doit exister et etre termine (memoire, sinon base).
		var orgID, fileName string
		ch := c.ChunkByID(chunkID)
		if ch != nil {
			if ch.Status != "done" {
				http.Error(w, "chunk non termine", http.StatusConflict)
				return
			}
			orgID = ch.OrgID
			fileName = ch.ExpectedOutput
		} else {
			dbOrg, dbStatus, dbOut := store.ChunkFromDB(chunkID)
			if dbStatus != "done" {
				http.Error(w, "chunk inconnu ou non termine", http.StatusNotFound)
				return
			}
			orgID, fileName = dbOrg, dbOut
		}
		if r.ContentLength > maxResultBytes {
			http.Error(w, "fichier trop volumineux (max 5 Go)", http.StatusRequestEntityTooLarge)
			return
		}
		// Garde-fou espace disque : le VPS a ~31 Go libres, un seul
		// gros rendu ne doit jamais remplir le disque du serveur.
		if free := freeBytes(resultsDir()); free < maxResultBytes+resultFreeFloor {
			dashLog("alerte", fmt.Sprintf("upload resultat REFUSE : espace disque insuffisant (%.1f Go libres)",
				float64(free)/(1<<30)))
			http.Error(w, "espace disque serveur insuffisant", http.StatusInsufficientStorage)
			return
		}
		// Ecriture en streaming (jamais en memoire) vers un temporaire,
		// puis renommage atomique.
		final := resultPath(chunkID)
		tmp := final + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			http.Error(w, "erreur ecriture", http.StatusInternalServerError)
			return
		}
		n, err := io.Copy(f, io.LimitReader(r.Body, maxResultBytes+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			os.Remove(tmp)
			http.Error(w, "erreur ecriture", http.StatusInternalServerError)
			return
		}
		if n > maxResultBytes {
			os.Remove(tmp)
			http.Error(w, "fichier trop volumineux (max 5 Go)", http.StatusRequestEntityTooLarge)
			return
		}
		if err := os.Rename(tmp, final); err != nil {
			os.Remove(tmp)
			http.Error(w, "erreur ecriture", http.StatusInternalServerError)
			return
		}
		name := fileName
		if name == "" {
			name = "result.bin"
		}
		sha := ""
		if ch != nil {
			sha = ch.ResultSHA256
		}
		store.SaveResult(ResultRow{
			ChunkID: chunkID, JobID: jobIDOfChunk(c, chunkID), OrgID: orgID,
			FileName: name, Size: n, SHA256: sha,
		})
		dashLog("result_upload", fmt.Sprintf("%s : fichier resultat %.2f Mo recupere par le serveur",
			chunkID, float64(n)/(1<<20)))
		writeJSON(w, map[string]interface{}{
			"chunk_id": chunkID,
			"stored":   true,
			"size":     n,
		})
	}
}

// jobIDOfChunk : job d'appartenance d'un chunk (memoire uniquement,
// le nom de fichier suffit sinon).
func jobIDOfChunk(c *Chunker, chunkID string) string {
	if i := strings.LastIndex(chunkID, "-"); i > 0 {
		return chunkID[:i]
	}
	return ""
}

// resultFileInfo : taille du fichier stocke, 0 si absent.
func resultFileInfo(chunkID string) int64 {
	st, err := os.Stat(resultPath(chunkID))
	if err != nil {
		return 0
	}
	return st.Size()
}

// GET /v1/results/<chunk_id> : le client telecharge son fichier.
func downloadResult(orgs *OrgStore, c *Chunker, store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		org := orgs.Authenticate(r)
		if org == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		chunkID := strings.TrimPrefix(r.URL.Path, "/v1/results/")
		if chunkID == "" || strings.Contains(chunkID, "/") {
			http.NotFound(w, r)
			return
		}
		// Droit d'acces : le chunk appartient a l'organisation.
		var fileName string
		allowed := false
		if ch := c.ChunkByID(chunkID); ch != nil {
			allowed = ch.OrgID == org.ID
			fileName = ch.ExpectedOutput
		}
		if !allowed {
			if row := store.LoadResult(chunkID); row != nil {
				allowed = row.OrgID == org.ID
				if fileName == "" {
					fileName = row.FileName
				}
			}
		}
		if !allowed {
			http.NotFound(w, r) // pas de fuite d'information
			return
		}
		path := resultPath(chunkID)
		if _, err := os.Stat(path); err != nil {
			http.Error(w, "resultat pas encore disponible", http.StatusNotFound)
			return
		}
		if fileName == "" {
			fileName = "result.bin"
		}
		w.Header().Set("Content-Disposition",
			`attachment; filename="`+fileName+`"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, path)
	}
}

// purgeResultsLoop : efface les fichiers de resultat expires (30
// jours) toutes les 6 heures, base + disque.
func purgeResultsLoop(store *Store) {
	for range time.Tick(6 * time.Hour) {
		ids := store.PurgeOldResults(resultRetention)
		n := 0
		for _, id := range ids {
			if err := os.Remove(resultPath(id)); err == nil {
				n++
			}
		}
		// Fichiers orphelins (upload sans base) : mtime expire aussi.
		entries, err := os.ReadDir(resultsDir())
		if err == nil {
			cutoff := time.Now().Add(-resultRetention)
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
					if os.Remove(filepath.Join(resultsDir(), e.Name())) == nil {
						n++
					}
				}
			}
		}
		if n > 0 {
			dashLog("result_purge", fmt.Sprintf("%d fichier(s) de resultat expires supprimes (retention 30 j)", n))
			log.Printf("purge resultats: %d fichiers supprimes", n)
		}
	}
}
