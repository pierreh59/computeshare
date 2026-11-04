package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Sprint 8 : persistance PostgreSQL (mode memoire si BDD absente).
	store := NewStore()
	fraud := NewFraud()
	registry := NewRegistry(store, fraud)
	hub := NewHub(registry)
	pricing := NewPricing(store)
	wallet := NewWallet(store)
	billing := NewBilling(store)
	chunker := NewChunker(registry, pricing, store, fraud, wallet, billing)
	orgs := NewOrgStore(store)
	accounts := NewAccountStore(store, orgs)
	orgs.SetAccountAuth(accounts.Authenticate)
	nodeAuth := NewNodeAuth(accounts)
	store.LoadAll(registry, pricing, chunker, orgs)
	store.LoadUsers(accounts)
	store.LoadWallet(wallet)
	store.LoadBilling(billing)
	wallet.Start()

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	http.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, registry.OnlineNodes())
	})

	// Sprint 15 (LAI-57) : comptes utilisateurs - inscription libre et
	// connexion, cote clients ET cote vendeurs.
	http.HandleFunc("/auth/signup", accounts.handleSignup)
	http.HandleFunc("/auth/login", accounts.handleLogin)
	http.HandleFunc("/auth/me", accounts.handleMe)
	http.HandleFunc("/auth/seller-token", accounts.handleSellerToken)
	// Sprint 15c (LAI-60) : gestion de compte - profil, mot de passe,
	// portabilite (export) et droit a l'effacement.
	http.HandleFunc("/auth/profile", withSession(accounts, accounts.handleProfile))
	http.HandleFunc("/auth/password", withSession(accounts, accounts.handlePassword))
	http.HandleFunc("/auth/export", withSession(accounts, accounts.handleExport(registry, wallet, billing)))
	http.HandleFunc("/auth/account", withSession(accounts, accounts.handleDeleteAccount(registry)))
	http.HandleFunc("/privacy", privacyPage)

	// Sprint 15 (LAI-58) : pages web des comptes.
	http.HandleFunc("/signup", signupPage)
	http.HandleFunc("/login", loginPage)
	http.HandleFunc("/me", mePage)
	http.HandleFunc("/seller", sellerPage)
	http.HandleFunc("/seller/nodes", accounts.handleSellerNodes(registry))
	http.HandleFunc("/seller/wallet", accounts.handleSellerWallet(registry, wallet))

	// Mini-console admin (cle X-Admin-Key, LAI-59).
	http.HandleFunc("/admin/console", adminPage)
	// Sprint 14 : les endpoints noeuds sont proteges par le jeton
	// d'enrolement (en-tete X-Node-Token, cf. nodeauth.go).
	http.HandleFunc("/register", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, sellerID string) {
		var msg struct {
			NodeID             string `json:"node_id"`
			GPUModel           string `json:"gpu_model"`
			GPUCategory        string `json:"gpu_category"`
			Zone               string `json:"zone"`
			MachineFingerprint string `json:"machine_fingerprint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil || msg.NodeID == "" {
			http.Error(w, "invalid register", http.StatusBadRequest)
			return
		}
		writeJSON(w, registry.Register(msg.NodeID, msg.GPUModel,
			msg.GPUCategory, msg.Zone, msg.MachineFingerprint))
		// Sprint 15 : jeton personnel -> le noeud devient la
		// propriete du compte vendeur.
		if sellerID != "" {
			registry.BindSeller(msg.NodeID, sellerID)
		}
	}))
	http.HandleFunc("/ws", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		nodeWS(registry, hub, w, r)
	}))
	http.HandleFunc("/jobs", submitJob(chunker))
	http.HandleFunc("/chunks", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		nextChunk(chunker).ServeHTTP(w, r)
	}))
	http.HandleFunc("/chunk_result", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		completeChunk(chunker).ServeHTTP(w, r)
	}))

	// LAI-51/52 : console admin (cle via en-tete X-Admin-Key).
	http.HandleFunc("/admin/pricing", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.Method {
		case "GET":
			writeJSON(w, map[string]interface{}{
				"current": pricing.Snapshot(),
				"floors":  FLOORS,
			})
		case "POST":
			var req struct {
				Category   string  `json:"category"`
				EurPerHour float64 `json:"eur_per_hour"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if err := pricing.SetPrice(req.Category, req.EurPerHour); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, pricing.Snapshot())
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/admin/alerts", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeJSON(w, pricing.Alerts())
	})

	// LAI-43/44 : alertes anti-fraude (Sybil, execution ralentie).
	http.HandleFunc("/admin/fraud", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeJSON(w, fraud.Alerts())
	})

	// Sprint 12 - Wallet vendeur (LAI-34/35/36).
	// Solde + historique du vendeur.
	http.HandleFunc("/wallet", walletBalance(wallet))
	// Simulation de retrait : frais affiches AVANT confirmation (LAI-35).
	http.HandleFunc("/wallet/quote", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		walletQuote(wallet).ServeHTTP(w, r)
	})
	// Demande de retrait (bloquee si KYC non approuve, LAI-36).
	http.HandleFunc("/wallet/withdraw", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		walletWithdraw(wallet).ServeHTTP(w, r)
	})
	// Admin : liste des retraits + passage en traite/echoue.
	http.HandleFunc("/admin/withdrawals", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.Method {
		case "GET":
			nodeID := r.URL.Query().Get("node_id")
			writeJSON(w, wallet.Withdrawals(nodeID))
		case "POST":
			var req struct {
				ID     string `json:"id"`
				Status string `json:"status"` // processed | failed
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if err := wallet.ProcessWithdrawal(req.ID, req.Status); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]string{"id": req.ID, "status": req.Status})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	// Admin : pilotage du KYC vendeur (LAI-36).
	http.HandleFunc("/admin/kyc", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.Method {
		case "GET":
			nodeID := r.URL.Query().Get("node_id")
			writeJSON(w, map[string]string{
				"node_id": nodeID,
				"kyc":     wallet.KYCStatus(nodeID),
			})
		case "POST":
			var req struct {
				NodeID string `json:"node_id"`
				Status string `json:"status"` // none | pending | approved | rejected
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if err := wallet.SetKYC(req.NodeID, req.Status); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]string{
				"node_id": req.NodeID,
				"kyc":     req.Status,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// LAI-29 : creation d'une organisation cliente (admin only).
	// La cle API n'est montree qu'une fois, a la creation.
	http.HandleFunc("/admin/orgs", func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		org, key, err := orgs.CreateOrg(req.Name)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]interface{}{
			"org_id":  org.ID,
			"name":    org.Name,
			"api_key": key,
		})
	})

	// LAI-30 : API v1 clients B2B (auth Bearer, cf. api/openapi.yaml).
	http.HandleFunc("/v1/jobs", v1Submit(orgs, chunker, billing))
	http.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			v1Status(orgs, chunker, billing).ServeHTTP(w, r)
		case strings.HasSuffix(r.URL.Path, "/results"):
			v1Results(orgs, chunker).ServeHTTP(w, r)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			v1Cancel(orgs, chunker).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// LAI-28 : object store des assets clients (auth Bearer).
	http.HandleFunc("/v1/assets", v1UploadAsset(orgs, store))
	http.HandleFunc("/v1/assets/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			v1DownloadAsset(orgs, store).ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Sprint 13 - Console B2B + billing (LAI-31/32).
	// Console client : formulaire de soumission + suivi live.
	http.HandleFunc("/console", consolePage)
	// Solde de credits et rapport d'usage du client authentifie.
	http.HandleFunc("/v1/balance", v1Balance(orgs, billing))
	http.HandleFunc("/v1/usage", v1Usage(orgs, billing))
	// Admin : vue billing, rechargement, remboursement.
	http.HandleFunc("/admin/billing", adminBilling(billing))
	http.HandleFunc("/admin/billing/topup", adminBillingTopUp(billing))
	http.HandleFunc("/admin/billing/refund", adminBillingRefund(billing))

	// Sprint 14 : TLS optionnel. Si TLS_CERT_FILE et TLS_KEY_FILE sont
	// definis (ex. Let's Encrypt ou certificat interne), l'orchestrateur
	// ecoute en HTTPS ; sinon HTTP (developpement local / derriere un
	// reverse proxy TLS comme Caddy ou nginx, recommande en production).
	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")
	if certFile != "" && keyFile != "" {
		log.Printf("orchestrator listening on :%s (HTTPS, TLS actif)", port)
		if err := http.ListenAndServeTLS(":"+port, certFile, keyFile, nil); err != nil {
			log.Fatal(err)
		}
		return
	}
	log.Printf("orchestrator listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
