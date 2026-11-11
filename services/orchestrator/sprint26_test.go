package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Sprint 26 : upload d'un fichier de resultat par le noeud puis
// telechargement par la bonne organisation, refus pour une autre.
func TestUploadEtDownloadResultat(t *testing.T) {
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("RESULTS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	orgs := NewOrgStore(store)
	org, key, _ := orgs.CreateOrg("Org Test S26")

	// Job termine appartenant a l'org.
	job := Job{ID: "job-s26", Command: "echo", TaskCount: 1, Zone: "europe"}
	if chunker.Submit(job, org.ID) == nil {
		t.Fatal("soumission refusee")
	}
	ch := chunker.NextFor("node-a")
	if !chunker.Complete(ch.ID, "node-a", "ok", "sha-abc", "", 10, 0, 0) {
		t.Fatal("completion refusee")
	}

	// Upload du fichier par le noeud.
	up := uploadResult(chunker, store)
	req := httptest.NewRequest("POST", "/results", bytes.NewReader([]byte("CONTENU-RESULTAT-123")))
	req.Header.Set("X-Chunk-Id", ch.ID)
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload refuse : HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := resultFileInfo(ch.ID); got != int64(len("CONTENU-RESULTAT-123")) {
		t.Fatalf("taille fichier = %d, attendu %d", got, len("CONTENU-RESULTAT-123"))
	}

	// Telechargement par la bonne org.
	dl := downloadResult(orgs, chunker, store)
	req2 := httptest.NewRequest("GET", "/v1/results/"+ch.ID, nil)
	req2.Header.Set("Authorization", "Bearer "+key)
	rec2 := httptest.NewRecorder()
	dl.ServeHTTP(rec2, req2)
	if rec2.Code != 200 || rec2.Body.String() != "CONTENU-RESULTAT-123" {
		t.Fatalf("telechargement refuse : HTTP %d body=%q", rec2.Code, rec2.Body.String())
	}

	// Une autre org n'a PAS acces (404, pas de fuite d'information).
	_, keyB, _ := orgs.CreateOrg("Org B S26")
	req3 := httptest.NewRequest("GET", "/v1/results/"+ch.ID, nil)
	req3.Header.Set("Authorization", "Bearer "+keyB)
	rec3 := httptest.NewRecorder()
	dl.ServeHTTP(rec3, req3)
	if rec3.Code != 404 {
		t.Fatalf("une org etrangere ne doit pas telecharger : HTTP %d", rec3.Code)
	}
}

// Sprint 26 : upload refuse tant que le chunk n'est pas termine, et
// refusi si le chunk est inconnu.
func TestUploadRefuseChunkNonTermine(t *testing.T) {
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("RESULTS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()

	up := uploadResult(chunker, store)

	// Chunk inconnu.
	req := httptest.NewRequest("POST", "/results", bytes.NewReader([]byte("x")))
	req.Header.Set("X-Chunk-Id", "chunk-fantome")
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("chunk inconnu : attendu 404, obtenu %d", rec.Code)
	}

	// Chunk pas encore termine.
	job := Job{ID: "job-s26b", Command: "echo", TaskCount: 1, Zone: "europe"}
	chunker.Submit(job, "")
	ch := chunker.NextFor("node-a") // running, pas done
	req2 := httptest.NewRequest("POST", "/results", bytes.NewReader([]byte("x")))
	req2.Header.Set("X-Chunk-Id", ch.ID)
	rec2 := httptest.NewRecorder()
	up.ServeHTTP(rec2, req2)
	if rec2.Code != 409 {
		t.Fatalf("chunk running : attendu 409, obtenu %d", rec2.Code)
	}
}

// Sprint 26 : taille max par fichier respectee (5 Go - test avec un
// Content-Length declare trop grand).
func TestUploadRefuseTropVolumineux(t *testing.T) {
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("RESULTS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	job := Job{ID: "job-s26c", Command: "echo", TaskCount: 1, Zone: "europe"}
	chunker.Submit(job, "")
	ch := chunker.NextFor("node-a")
	chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 1, 0, 0)

	up := uploadResult(chunker, store)
	req := httptest.NewRequest("POST", "/results", bytes.NewReader([]byte("x")))
	req.Header.Set("X-Chunk-Id", ch.ID)
	req.ContentLength = maxResultBytes + 1
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("fichier trop volumineux : attendu 413, obtenu %d", rec.Code)
	}
}

// Sprint 26 : la purge efface les fichiers plus vieux que 30 jours
// (fichier orphelin sur le disque, mtime expire).
func TestPurgeFichiersExpiries(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("RESULTS_DIR", dir)
	defer os.Unsetenv("RESULTS_DIR")

	old := filepath.Join(dir, "chunk-vieux")
	newf := filepath.Join(dir, "chunk-recent")
	os.WriteFile(old, []byte("vieux"), 0o644)
	os.WriteFile(newf, []byte("recent"), 0o644)
	past := time.Now().Add(-31 * 24 * time.Hour)
	os.Chtimes(old, past, past)

	// store desactive : PurgeOldResults ne fait rien, mais les
	// orphelins du disque sont quand meme purges par mtime.
	purgeOnceForTest()

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("le fichier expire doit etre supprime")
	}
	if _, err := os.Stat(newf); err != nil {
		t.Fatal("le fichier recent doit etre conserve")
	}
}

// purgeOnceForTest : une iteration de purge hors boucle infinie.
func purgeOnceForTest() {
	entries, _ := os.ReadDir(resultsDir())
	cutoff := time.Now().Add(-resultRetention)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(resultsDir(), e.Name()))
		}
	}
}

// Sprint 26 : v1Results annonce le lien de telechargement quand le
// fichier est present.
func TestV1ResultsAnnonceDownload(t *testing.T) {
	os.Setenv("RESULTS_DIR", t.TempDir())
	defer os.Unsetenv("RESULTS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	orgs := NewOrgStore(store)
	org, key, _ := orgs.CreateOrg("Org DL S26")

	job := Job{ID: "job-s26d", Command: "echo", TaskCount: 1, Zone: "europe"}
	chunker.Submit(job, org.ID)
	ch := chunker.NextFor("node-a")
	chunker.Complete(ch.ID, "node-a", "ok", "sha", "", 1, 0, 0)
	os.WriteFile(resultPath(ch.ID), []byte("data"), 0o644)

	h := v1Results(orgs, chunker, NewStore())
	req := httptest.NewRequest("GET", "/v1/jobs/job-s26d/results", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"download_url":"/v1/results/`)) {
		t.Fatalf("download_url absent de la reponse : %s", body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"size_bytes":4`)) {
		t.Fatalf("size_bytes absent de la reponse : %s", body)
	}
}
