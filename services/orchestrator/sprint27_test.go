package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Sprint 27 : un client televerse un asset, la soumission d'un job
// qui reference l'asset d'UNE AUTRE organisation est refusee.
func TestSoumissionAssetEtranger(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	orgA, keyA, _ := orgs.CreateOrg("Org A S27")
	orgB, keyB, _ := orgs.CreateOrg("Org B S27")
	billing := NewBilling(store)
	billing.TopUp(orgA.ID, 5)
	billing.TopUp(orgB.ID, 5)

	// L'org A televerse un asset.
	up := v1UploadAsset(orgs, assets)
	req := httptest.NewRequest("POST", "/v1/assets?filename=donnees.csv", bytes.NewReader([]byte("A,B\n1,2")))
	req.Header.Set("Authorization", "Bearer "+keyA)
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("upload refuse : HTTP %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "asset-") {
		t.Fatalf("reponse inattendue : %s", rec.Body.String())
	}
	id := strings.Split(strings.Split(rec.Body.String(), `"asset_id":"`)[1], `"`)[0]

	body := `{"id":"job-s27-vol","runtime":"allowlist","command":"echo","task_count":1,"asset_ids":["` + id + `"]}`

	// L'org B reference l'asset de A : refuse.
	sub := v1Submit(orgs, assets, chunker, billing)
	reqB := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	reqB.Header.Set("Authorization", "Bearer "+keyB)
	recB := httptest.NewRecorder()
	sub.ServeHTTP(recB, reqB)
	if recB.Code != 400 {
		t.Fatalf("asset etrange doit etre refuse, obtenu HTTP %d %s", recB.Code, recB.Body.String())
	}

	// L'org A reference son propre asset : accepte.
	reqA := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader([]byte(body)))
	reqA.Header.Set("Authorization", "Bearer "+keyA)
	recA := httptest.NewRecorder()
	sub.ServeHTTP(recA, reqA)
	if recA.Code != 200 {
		t.Fatalf("asset legitime refuse : HTTP %d %s", recA.Code, recA.Body.String())
	}
}

// Sprint 27 : le chunk livre au noeud embarque le manifeste des
// assets (id, nom de fichier, sha256, taille).
func TestManifestEmbarqueDansChunk(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	org, keyA, _ := orgs.CreateOrg("Org A Manifest")

	// Televerse puis soumet avec l'asset.
	up := v1UploadAsset(orgs, assets)
	req := httptest.NewRequest("POST", "/v1/assets?filename=entree.txt", bytes.NewReader([]byte("BONJOUR-ASSET")))
	req.Header.Set("Authorization", "Bearer "+keyA)
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	id := strings.Split(strings.Split(rec.Body.String(), `"asset_id":"`)[1], `"`)[0]

	job := Job{ID: "job-s27-man", Command: "echo", TaskCount: 1, AssetIDs: []string{id}}
	if chunker.Submit(job, org.ID) == nil {
		t.Fatal("soumission refusee")
	}

	ch := deliverChunk(chunker, assets, "node-a")
	if ch == nil {
		t.Fatal("chunk non livre")
	}
	if len(ch.Assets) != 1 {
		t.Fatalf("manifeste vide, attendu 1 asset, obtenu %d", len(ch.Assets))
	}
	a := ch.Assets[0]
	if a.AssetID != id || a.Filename != "entree.txt" {
		t.Fatalf("manifeste faux : %+v", a)
	}
	if a.SizeBytes != int64(len("BONJOUR-ASSET")) {
		t.Fatalf("taille fausse : %d", a.SizeBytes)
	}
	if a.SHA256 == "" {
		t.Fatal("sha256 absent du manifeste")
	}
}

// Sprint 27 : le noeud qui execute le chunk telecharge l'asset, un
// autre noeud (ou un asset non reference) est refuse.
func TestNodeDownloadAsset(t *testing.T) {
	os.Setenv("ASSETS_DIR", t.TempDir())
	defer os.Unsetenv("ASSETS_DIR")

	reg, chunker := newTestDash(t)
	reg.Register("node-a", "GPU A", "prosumer", "europe", "fp-a", false, false)
	store := NewStore()
	assets := NewAssetStore(store)
	orgs := NewOrgStore(store)
	org, keyA, _ := orgs.CreateOrg("Org A NodeDL")
	_, _, _ = orgs.CreateOrg("Org B NodeDL")

	// Asset de l'org A.
	up := v1UploadAsset(orgs, assets)
	req := httptest.NewRequest("POST", "/v1/assets?filename=entree.txt", bytes.NewReader([]byte("CONTENU-ENTREE")))
	req.Header.Set("Authorization", "Bearer "+keyA)
	rec := httptest.NewRecorder()
	up.ServeHTTP(rec, req)
	id := strings.Split(strings.Split(rec.Body.String(), `"asset_id":"`)[1], `"`)[0]

	// Job avec l'asset, livre au noeud (statut running).
	job := Job{ID: "job-s27-dl", Command: "echo", TaskCount: 1, AssetIDs: []string{id}}
	chunker.Submit(job, org.ID)
	ch := deliverChunk(chunker, assets, "node-a")
	if ch == nil {
		t.Fatal("chunk non livre")
	}

	na := NewNodeAuth(NewAccountStore(store, orgs))
	route := na.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		v1NodeDownloadAsset(assets, chunker).ServeHTTP(w, r)
	})
	url := "/v1/assets/" + id + "/node-download?node_id=node-a&chunk_id=" + ch.ID

	// Sans jeton : 401.
	req0 := httptest.NewRequest("GET", url, nil)
	rec0 := httptest.NewRecorder()
	route.ServeHTTP(rec0, req0)
	if rec0.Code != 401 {
		t.Fatalf("sans jeton : attendu 401, obtenu %d", rec0.Code)
	}

	// Bon noeud : 200 + contenu + sha.
	req1 := httptest.NewRequest("GET", url, nil)
	req1.Header.Set("X-Node-Token", "dev-node-token")
	rec1 := httptest.NewRecorder()
	route.ServeHTTP(rec1, req1)
	if rec1.Code != 200 || rec1.Body.String() != "CONTENU-ENTREE" {
		t.Fatalf("telechargement noeud : HTTP %d body=%q", rec1.Code, rec1.Body.String())
	}
	if rec1.Header().Get("X-Content-Sha256") == "" {
		t.Fatal("sha256 absent de la reponse")
	}

	// Mauvais noeud : 404.
	req2 := httptest.NewRequest("GET", "/v1/assets/"+id+"/node-download?node_id=node-z&chunk_id="+ch.ID, nil)
	req2.Header.Set("X-Node-Token", "dev-node-token")
	rec2 := httptest.NewRecorder()
	route.ServeHTTP(rec2, req2)
	if rec2.Code != 404 {
		t.Fatalf("noeud etranger : attendu 404, obtenu %d", rec2.Code)
	}

	// Asset non reference par le chunk : 404.
	req3 := httptest.NewRequest("GET", "/v1/assets/asset-fantome0000/node-download?node_id=node-a&chunk_id="+ch.ID, nil)
	req3.Header.Set("X-Node-Token", "dev-node-token")
	rec3 := httptest.NewRecorder()
	route.ServeHTTP(rec3, req3)
	if rec3.Code != 404 {
		t.Fatalf("asset non reference : attendu 404, obtenu %d", rec3.Code)
	}
}
