package main

// Sprint 39 : tests de la decision produit "tous les jobs en GPU"
// (le serveur force job.Gpu meme si le client n'envoie pas "gpu")
// et des liens de telechargement des resultats (jeton HMAC dans
// l'e-mail job termine + endpoint admin /admin/dash/result/).

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Un job soumis SANS champ gpu doit etre traite comme un job GPU :
// sans noeud gpu_ready en ligne, refus 503 "no gpu-capable node".
func TestSubmitSansGPUEstForceGPU(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("ASSETS_DIR", t.TempDir())
	os.Setenv("RESULTS_DIR", t.TempDir())

	reg, chunker := newTestDash(t)
	// Un seul noeud, NON gpu_ready : un job CPU y passerait, un job GPU non.
	reg.Register("node-cpu39", "GPU A", "prosumer", "europe", "fp-a", false, false)

	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	billing := NewBilling(store)

	u, key, _, code := accounts.SignUp("client39@computeshare.fr", "MotDePasse39!", "client", "Client 39", true, nil)
	if u == nil || code != 201 {
		t.Fatalf("inscription refusee : HTTP %d", code)
	}
	raw := accounts.newAuthToken(u.ID, "verify_email", u.Email)
	v := httptest.NewRequest("GET", "/verify?token="+raw, nil)
	accounts.handleVerify(httptest.NewRecorder(), v)
	if !u.EmailVerified {
		t.Fatal("le compte doit etre verifie")
	}
	billing.TopUp(u.OrgID, 5)

	sub := v1Submit(orgs, assets, chunker, billing, accounts)
	body := `{"id":"job-s39-a","runtime":"allowlist","command":"echo","task_count":1}`

	req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	sub.ServeHTTP(rec, req)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "no gpu-capable node") {
		t.Fatalf("sans noeud gpu_ready, un job sans champ gpu doit etre refuse 503 GPU : HTTP %d %s", rec.Code, rec.Body.String())
	}

	// Un noeud gpu_ready arrive : le meme job part, en GPU.
	reg.Register("node-gpu39", "GPU B", "prosumer", "europe", "fp-b", false, true)
	req2 := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	req2.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	sub.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("soumission attendue 200 : HTTP %d %s", rec2.Code, rec2.Body.String())
	}
	ch := chunker.NextFor("node-gpu39")
	if ch == nil {
		t.Fatal("le chunk doit etre livre au noeud gpu_ready")
	}
	if !ch.Gpu {
		t.Fatalf("le chunk %s doit heriter du gpu force par le serveur", ch.ID)
	}
	if chunker.NextFor("node-cpu39") != nil {
		t.Fatal("aucun chunk ne doit aller vers un noeud non gpu_ready")
	}
}

// Lien signe Sprint 39 : GET /v1/results/<id>?t=<token> sans session
// renvoie le fichier ; mauvais jeton ou expire -> 401.
func TestDownloadParJeton(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("ADMIN_KEY", "")
	os.Setenv("RESULTS_DIR", t.TempDir())

	_, chunker := newTestDash(t)
	store := NewStore()
	orgs := NewOrgStore(store)

	chunkID := "chunk-s39-dl"
	if err := os.WriteFile(resultPath(chunkID), []byte("contenu-du-resultat-s39"), 0o640); err != nil {
		t.Fatalf("ecriture du fichier resultat : %v", err)
	}

	h := downloadResult(orgs, chunker, store)

	get := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		h(w, req)
		return w
	}

	tok := resultDownloadToken(chunkID, time.Now().Add(7*24*time.Hour))
	w := get("/v1/results/" + chunkID + "?t=" + tok)
	if w.Code != 200 || w.Body.String() != "contenu-du-resultat-s39" {
		t.Fatalf("telechargement par jeton attendu 200 avec contenu : HTTP %d %q", w.Code, w.Body.String())
	}

	// Jeton pour un autre chunk : refuse.
	w = get("/v1/results/" + chunkID + "?t=" + resultDownloadToken("chunk-autre", time.Now().Add(time.Hour)))
	if w.Code != 401 {
		t.Fatalf("jeton d'un autre chunk : 401 attendu, recu %d", w.Code)
	}

	// Jeton expire : refuse.
	w = get("/v1/results/" + chunkID + "?t=" + resultDownloadToken(chunkID, time.Now().Add(-time.Minute)))
	if w.Code != 401 {
		t.Fatalf("jeton expire : 401 attendu, recu %d", w.Code)
	}
}

// Endpoint admin : sans cle 403, avec cle 200.
func TestAdminDownloadResult(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("ADMIN_KEY", "cle-admin-s39")
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("ADMIN_KEY")

	_, chunker := newTestDash(t)
	store := NewStore()

	chunkID := "chunk-s39-admin"
	if err := os.WriteFile(resultPath(chunkID), []byte("resultat-admin-s39"), 0o640); err != nil {
		t.Fatalf("ecriture du fichier resultat : %v", err)
	}

	h := adminDownloadResult(chunker, store)

	req := httptest.NewRequest("GET", "/admin/dash/result/"+chunkID, nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 403 {
		t.Fatalf("sans cle admin : 403 attendu, recu %d", rec.Code)
	}

	req2 := httptest.NewRequest("GET", "/admin/dash/result/"+chunkID+"?key=cle-admin-s39", nil)
	rec2 := httptest.NewRecorder()
	h(rec2, req2)
	if rec2.Code != 200 || rec2.Body.String() != "resultat-admin-s39" {
		t.Fatalf("avec cle admin : 200 attendu avec contenu, recu %d %q", rec2.Code, rec2.Body.String())
	}
}

// BuildResultLinks : l'e-mail job termine contient des liens signes
// pour chaque tache reussie, et DashRecentResults remonte le chunk.
func TestBuildResultLinksEtRecentResults(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("RESULTS_DIR", t.TempDir())
	os.Setenv("DOWNLOAD_BASE_URL", "https://app-test.computeshare.example")

	reg, chunker := newTestDash(t)
	reg.Register("node-a39", "GPU A", "prosumer", "europe", "fp-a", true, true)

	job := Job{ID: "job-s39-links", Command: "echo", TaskCount: 1,
		Runtime: "allowlist", Gpu: true, OutputName: "out-s39.txt"}
	if chunker.Submit(job, "org-s39") == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a39")
	if ch == nil {
		t.Fatal("chunk non livre")
	}
	chunkID := ch.ID
	if !chunker.Complete(chunkID, "node-a39", "ok", "deadbeef", "", 10, 100, 200) {
		t.Fatal("complete refuse")
	}

	links := BuildResultLinks("https://app-test.computeshare.example", []Chunk{*ch})
	if len(links) != 1 {
		t.Fatalf("1 lien attendu, recu %d", len(links))
	}
	l := links[0]
	if l.ChunkID != chunkID || l.FileName != "out-s39.txt" ||
		!strings.HasPrefix(l.URL, "https://app-test.computeshare.example/v1/results/"+chunkID+"?t=") {
		t.Fatalf("lien inattendu : %+v", l)
	}
	// Le jeton du lien est valide.
	tok := l.URL[strings.Index(l.URL, "t=")+2:]
	if !validResultToken(chunkID, tok) {
		t.Fatal("le jeton du lien doit etre valide")
	}

	// DashRecentResults remonte le chunk termine.
	rr := chunker.DashRecentResults(12)
	if len(rr) != 1 || rr[0]["chunk_id"] != chunkID || rr[0]["file"] != "out-s39.txt" {
		t.Fatalf("DashRecentResults inattendu : %v (json %s)", rr, func() string { b, _ := json.Marshal(rr); return string(b) }())
	}
}

// Sprint 39b : la console client recupere un jeton signe + la date de
// fin de dispo depuis /v1/jobs/<id>/results, et telecharge sans session.
func TestClientResultsLien(t *testing.T) {
	os.Setenv("DATABASE_URL", "")
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("RESULTS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a39b", "GPU A", "prosumer", "europe", "fp-a", false, true)
	store := NewStore()
	orgs := NewOrgStore(store)
	org, key, _ := orgs.CreateOrg("Org S39b")

	job := Job{ID: "job-s39b", Command: "echo", TaskCount: 1, Gpu: true, OutputName: "out-s39b.txt"}
	if chunker.Submit(job, org.ID) == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a39b")
	if ch == nil {
		t.Fatal("chunk non livre")
	}
	if !chunker.Complete(ch.ID, "node-a39b", "ok", "sha-s39b", "", 1, 0, 0) {
		t.Fatal("complete refuse")
	}
	if err := os.WriteFile(resultPath(ch.ID), []byte("donnees-s39b"), 0o644); err != nil {
		t.Fatalf("ecriture resultat : %v", err)
	}

	// Reponse authentifiee : jeton + date de fin de dispo.
	h := v1Results(orgs, chunker, store)
	req := httptest.NewRequest("GET", "/v1/jobs/job-s39b/results", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var rows []map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("1 ligne attendue : %s", rec.Body.String())
	}
	tok, _ := rows[0]["download_token"].(string)
	if tok == "" {
		t.Fatalf("download_token absent : %s", rec.Body.String())
	}
	if rows[0]["file"] != "out-s39b.txt" || rows[0]["status"] != "done" {
		t.Fatalf("file/status absents : %s", rec.Body.String())
	}
	until, _ := rows[0]["available_until"].(string)
	if want := time.Now().Add(resultRetention).Format("02/01/2006"); until != want {
		t.Fatalf("available_until = %q, attendu %q", until, want)
	}

	// Le lien du jeton telecharge SANS session (clic direct console).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/v1/results/"+ch.ID+"?t="+tok, nil)
	downloadResult(orgs, chunker, store).ServeHTTP(rec2, req2)
	if rec2.Code != 200 || rec2.Body.String() != "donnees-s39b" {
		t.Fatalf("telechargement par lien console attendu 200 : HTTP %d %q", rec2.Code, rec2.Body.String())
	}
}
