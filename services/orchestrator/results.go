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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	// Fix 7 nov. 2026 : l'echec etait avale ; en prod le volume
	// etait root:root et l'app (uid 1000) ne pouvait rien creer.
	if err := os.MkdirAll(d, 0o750); err != nil {
		log.Printf("results: creation de %s impossible : %v", d, err)
	}
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
		// Sprint 26b : tracer TOUTE tentative, meme refusee, pour
		// diagnostiquer a distance (le noeud n'affiche pas l'erreur).
		log.Printf("POST /results chunk=%s bytes=%d", chunkID, r.ContentLength)
		if chunkID == "" {
			http.Error(w, "X-Chunk-Id manquant", http.StatusBadRequest)
			return
		}
		// Le chunk doit exister et etre termine (memoire, sinon base).
		var orgID, fileName string
		ch := c.ChunkByID(chunkID)
		if ch != nil {
			if ch.Status != "done" {
				dashLog("result_upload", chunkID+" refuse : chunk non termine (409)")
				http.Error(w, "chunk non termine", http.StatusConflict)
				return
			}
			orgID = ch.OrgID
			fileName = ch.ExpectedOutput
		} else {
			dbOrg, dbStatus, dbOut := store.ChunkFromDB(chunkID)
			if dbStatus != "done" {
				dashLog("result_upload", chunkID+" refuse : chunk inconnu ou non termine (404)")
				http.Error(w, "chunk inconnu ou non termine", http.StatusNotFound)
				return
			}
			orgID, fileName = dbOrg, dbOut
		}
		if r.ContentLength > maxResultBytes {
			dashLog("result_upload", chunkID+" refuse : trop volumineux (413)")
			http.Error(w, "fichier trop volumineux (max 5 Go)", http.StatusRequestEntityTooLarge)
			return
		}
		// Garde-fou espace disque : le VPS a ~31 Go libres, un seul
		// gros rendu ne doit jamais remplir le disque du serveur.
		if free := freeBytes(resultsDir()); free < maxResultBytes+resultFreeFloor {
			dashLog("alerte", fmt.Sprintf("upload resultat REFUSE : espace disque insuffisant (%.1f Go libres)",
				float64(free)/(1<<30)))
			dashLog("result_upload", chunkID+" refuse : espace disque insuffisant (507)")
			http.Error(w, "espace disque serveur insuffisant", http.StatusInsufficientStorage)
			return
		}
		// Ecriture en streaming (jamais en memoire) vers un temporaire,
		// puis renommage atomique.
		final := resultPath(chunkID)
		tmp := final + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			// Fix 7 nov. 2026 : le detail remonte au noeud ET aux
			// logs serveur (l'erreur muette "erreur ecriture" nous
			// a coute un debug de plusieurs heures).
			log.Printf("results: creation de %s impossible : %v", tmp, err)
			http.Error(w, "erreur ecriture : "+err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := io.Copy(f, io.LimitReader(r.Body, maxResultBytes+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			log.Printf("results: ecriture de %s impossible : copy=%v close=%v", tmp, err, closeErr)
			os.Remove(tmp)
			http.Error(w, "erreur ecriture : "+fmt.Sprint(err, closeErr), http.StatusInternalServerError)
			return
		}
		if n > maxResultBytes {
			os.Remove(tmp)
			http.Error(w, "fichier trop volumineux (max 5 Go)", http.StatusRequestEntityTooLarge)
			return
		}
		if err := os.Rename(tmp, final); err != nil {
			log.Printf("results: renommage de %s vers %s impossible : %v", tmp, final, err)
			os.Remove(tmp)
			http.Error(w, "erreur ecriture : "+err.Error(), http.StatusInternalServerError)
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

// resultAvailableUntil : date a laquelle le fichier resultat sera
// purge (retention 30 jours). Base : heure de fin du chunk si connue,
// sinon date de modification du fichier sur le disque.
func resultAvailableUntil(base time.Time, chunkID string) string {
	if base.IsZero() {
		st, err := os.Stat(resultPath(chunkID))
		if err != nil {
			return ""
		}
		base = st.ModTime()
	}
	return base.Add(resultRetention).Format("02/01/2006")
}

// GET /v1/results/<chunk_id> : le client telecharge son fichier.
func downloadResult(orgs *OrgStore, c *Chunker, store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		chunkID := strings.TrimPrefix(r.URL.Path, "/v1/results/")
		if chunkID == "" || strings.Contains(chunkID, "/") {
			http.NotFound(w, r)
			return
		}
		org := orgs.Authenticate(r)
		var fileName string
		allowed := false
		if org != nil {
			// Droit d'acces : le chunk appartient a l'organisation.
			if ch := c.ChunkByID(chunkID); ch != nil {
				allowed = ch.OrgID == org.ID
				fileName = ch.ExpectedOutput
			}
		} else if validResultToken(chunkID, r.URL.Query().Get("t")) {
			// Sprint 39 : lien signe (e-mail job termine), valable 7 j.
			allowed = true
			if ch := c.ChunkByID(chunkID); ch != nil {
				fileName = ch.ExpectedOutput
			}
		}
		if !allowed && org != nil {
			if row := store.LoadResult(chunkID); row != nil {
				allowed = row.OrgID == org.ID
				if fileName == "" {
					fileName = row.FileName
				}
			}
		}
		// Sprint 39c (retour Pierre) : apres un redemarrage le chunk
		// n'est plus en memoire, le nom du fichier vient alors des
		// resultats persistes (sinon le telechargement livrait
		// result.bin alors que le nom est bien connu en base).
		if allowed && fileName == "" {
			if row := store.LoadResult(chunkID); row != nil {
				fileName = row.FileName
			}
		}
		if !allowed {
			if org == nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			} else {
				http.NotFound(w, r) // pas de fuite d'information
			}
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

// POST /chunk_upload : compte-rendu du rapatriement envoye par le noeud
// (Sprint 26c). Diagnostic seulement : chaque upload reussi, refuse ou
// meme non tente est journalise cote serveur, ou l'UI du noeud ne
// suffit pas (echec silencieux constate en prod).
func reportUploadNote() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChunkID string `json:"chunk_id"`
			NodeID  string `json:"node_id"`
			Note    string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChunkID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if len(req.Note) > 300 {
			req.Note = req.Note[:300]
		}
		msg := req.ChunkID + " rapatriement par " + req.NodeID + " : " + req.Note
		if req.Note == "" {
			msg += "(aucun compte-rendu, chunk non ok ?)"
		}
		dashLog("result_upload", msg)
		writeJSON(w, map[string]bool{"recorded": true})
	}
}

// ------------------------------------------------------------------
// Sprint 39 : liens de telechargement signes (e-mail job termine) et
// route admin. Un e-mail ne peut pas porter d'en-tete Authorization :
// les liens de telechargement embarquent un jeton HMAC signe par le
// serveur (SESSION_SECRET ou ADMIN_KEY derive), valable 7 jours.

const resultLinkTTL = 7 * 24 * time.Hour

// downloadSecret : cle de signature des liens (jamais exposee).
func downloadSecret() []byte {
	b := os.Getenv("SESSION_SECRET")
	if b == "" {
		b = os.Getenv("ADMIN_KEY")
	}
	if b == "" {
		b = "dev-download-secret"
	}
	h := sha256.Sum256([]byte(b))
	return h[:]
}

// resultDownloadToken : "<expiration unix>.<hmac>" pour un chunk.
func resultDownloadToken(chunkID string, exp time.Time) string {
	e := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, downloadSecret())
	mac.Write([]byte("dl|" + chunkID + "|" + e))
	return e + "." + hex.EncodeToString(mac.Sum(nil))
}

// ResultLink : un lien de telechargement prete a inserrer dans un
// e-mail (Sprint 39).
type ResultLink struct {
	ChunkID  string
	FileName string
	URL      string
}

// validResultToken : signature correcte et lien non expire.
func validResultToken(chunkID, token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, downloadSecret())
	mac.Write([]byte("dl|" + chunkID + "|" + parts[0]))
	return hmac.Equal([]byte(parts[1]), []byte(hex.EncodeToString(mac.Sum(nil))))
}

// BuildResultLinks : liens signes (7 jours) pour les chunks done d un
// job, avec le nom de fichier attendu.
func BuildResultLinks(baseURL string, chunks []Chunk) []ResultLink {
	exp := time.Now().Add(resultLinkTTL)
	var out []ResultLink
	for _, ch := range chunks {
		if ch.Status != "done" {
			continue
		}
		name := ch.ExpectedOutput
		if name == "" {
			name = "result.bin"
		}
		out = append(out, ResultLink{
			ChunkID:  ch.ID,
			FileName: name,
			URL:      baseURL + "/v1/results/" + ch.ID + "?t=" + resultDownloadToken(ch.ID, exp),
		})
	}
	return out
}

// GET /admin/dash/result/<chunk_id> : telechargement d'un resultat
// par l'admin (Sprint 39). La cle est acceptee en en-tete X-Admin-Key
// ou en parametre ?key= (lien simple dans le dashboard).
func adminDownloadResult(c *Chunker, store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Admin-Key")
		if key == "" {
			key = r.URL.Query().Get("key")
		}
		r2 := &http.Request{Header: http.Header{"X-Admin-Key": []string{key}}}
		if !requireAdmin(r2) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		chunkID := strings.TrimPrefix(r.URL.Path, "/admin/dash/result/")
		if chunkID == "" || strings.Contains(chunkID, "/") {
			http.NotFound(w, r)
			return
		}
		var fileName string
		if ch := c.ChunkByID(chunkID); ch != nil {
			fileName = ch.ExpectedOutput
		}
		if fileName == "" {
			if row := store.LoadResult(chunkID); row != nil {
				fileName = row.FileName
			}
		}
		path := resultPath(chunkID)
		if _, err := os.Stat(path); err != nil {
			http.Error(w, "resultat plus disponible (purge 30 jours ou jamais de posse)", http.StatusNotFound)
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
