package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// orchestratorVersion : version de l'orchestrateur, affichee sur le
// dashboard admin et dans /healthz. A incrementer a chaque sprint.
const orchestratorVersion = "v0.25.1"

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
	// Sprint 27 : metadonnees des assets clients (entrees de job).
	assets := NewAssetStore(store)
	accounts := NewAccountStore(store, orgs)
	orgs.SetAccountAuth(accounts.Authenticate)
	nodeAuth := NewNodeAuth(accounts)
	// Sprint 24 : le journal du dashboard admin est persiste en base.
	setDashStore(store)
	store.LoadAll(registry, pricing, chunker, orgs)
	store.LoadUsers(accounts)
	for _, t := range store.LoadAuthTokens() {
		accounts.RestoreAuthToken(t.ID, t.UserID, t.Kind, t.Hash, t.ExpiresAt)
	}
	accounts.emails = NewEmailer()
	store.LoadWallet(wallet)
	store.LoadBilling(billing)
	wallet.Start()

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok "+orchestratorVersion)
	})
	http.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, registry.OnlineNodes())
	})

	// Audit du 8 nov. : la racine de l'API redirige vers la vitrine
	// au lieu d'un 404 nu (les routes specifiques restent prioritaires).
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://computeshare.fr", http.StatusFound)
	})

	// Sprint 15 (LAI-57) : comptes utilisateurs - inscription libre et
	// connexion, cote clients ET cote vendeurs.
	// Sprint 29 : verification email + reinitialisation mot de passe.
	http.HandleFunc("/auth/forgot_password", accounts.handleForgotPassword)
	http.HandleFunc("/auth/reset_password", accounts.handleResetPassword)
	http.HandleFunc("/auth/resend_verification", withSession(accounts, accounts.handleResendVerification))
	http.HandleFunc("/verify", accounts.handleVerify)
	http.HandleFunc("/forgot", forgotPage)
	http.HandleFunc("/reset", resetPage)

	http.HandleFunc("/auth/signup", accounts.handleSignup)
	http.HandleFunc("/auth/login", accounts.handleLogin)
	http.HandleFunc("/auth/me", accounts.handleMe)
	http.HandleFunc("/auth/seller-token", accounts.handleSellerToken)
	// Sprint 15c (LAI-60) : gestion de compte - profil, mot de passe,
	// portabilite (export) et droit a l'effacement.
	http.HandleFunc("/auth/profile", withSession(accounts, accounts.handleProfile))
	// Sprint 15d (LAI-61) : declaration societe (SIRET/TVA + verif SIRENE).
	http.HandleFunc("/auth/company", withSession(accounts, accounts.handleCompany))
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
	// Sprint 17 : dashboard admin temps reel (LAI-63).
	http.HandleFunc("/admin/dashboard", dashboardPage)
	http.HandleFunc("/admin/dash/data", adminDashData(registry, chunker, fraud, accounts, orgs))
	http.HandleFunc("/admin/dash/ban", adminDashBan(registry, chunker))
	http.HandleFunc("/admin/dash/unban", adminDashUnban(registry))
	http.HandleFunc("/admin/dash/cancel", adminDashCancel(chunker))
	// Sprint 14 : les endpoints noeuds sont proteges par le jeton
	// d'enrolement (en-tete X-Node-Token, cf. nodeauth.go).
	http.HandleFunc("/register", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, sellerID string) {
		var msg struct {
			NodeID             string `json:"node_id"`
			GPUModel           string `json:"gpu_model"`
			GPUCategory        string `json:"gpu_category"`
			Zone               string `json:"zone"`
			MachineFingerprint string `json:"machine_fingerprint"`
			WSL2Ready          bool   `json:"wsl2_ready"`
			GpuReady           bool   `json:"gpu_ready"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil || msg.NodeID == "" {
			http.Error(w, "invalid register", http.StatusBadRequest)
			return
		}
		etaitEnLigne := registry.IsOnline(msg.NodeID)
		writeJSON(w, registry.Register(msg.NodeID, msg.GPUModel,
			msg.GPUCategory, msg.Zone, msg.MachineFingerprint, msg.WSL2Ready, msg.GpuReady))
		registry.SetNodeIP(msg.NodeID, clientIP(r))
		if !etaitEnLigne {
			if registry.IsOnline(msg.NodeID) {
				dashLog("node_register", "noeud "+msg.NodeID+" en ligne ("+msg.GPUModel+", zone "+msg.Zone+")")
			} else {
				dashLog("fraud", "noeud "+msg.NodeID+" tente de s'enregistrer mais il est bloque par l'admin")
			}
		}
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
		// Sprint 27 : le chunk livre embarque le manifeste des assets.
		nextChunkWithAssets(chunker, assets).ServeHTTP(w, r)
	}))
	http.HandleFunc("/chunk_result", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		completeChunk(chunker).ServeHTTP(w, r)
	}))
	// Sprint 26 : le noeud pousse le fichier de resultat du chunk.
	http.HandleFunc("/results", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		uploadResult(chunker, store).ServeHTTP(w, r)
	}))
	// Sprint 26c : compte-rendu du rapatriement (diagnostic).
	http.HandleFunc("/chunk_upload", nodeAuth.withNodeToken(func(w http.ResponseWriter, r *http.Request, _ string) {
		reportUploadNote().ServeHTTP(w, r)
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
				"current":     pricing.Snapshot(),
				"floors":      FLOORS,
				"cpu_current": pricing.SnapshotCPU(),
				"cpu_floors":  CPU_FLOORS,
			})
		case "POST":
			var req struct {
				Category   string  `json:"category"`
				EurPerHour float64 `json:"eur_per_hour"`
				// Sprint 25 : "cpu" pour un tarif CPU, vide (ou "gpu")
				// pour un tarif GPU (defaut, compatibilite admin).
				Kind string `json:"kind"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid", http.StatusBadRequest)
				return
			}
			if req.Kind == "cpu" {
				if err := pricing.SetCPUPrice(req.Category, req.EurPerHour); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			} else {
				if err := pricing.SetPrice(req.Category, req.EurPerHour); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
			writeJSON(w, map[string]interface{}{
				"current":     pricing.Snapshot(),
				"cpu_current": pricing.SnapshotCPU(),
			})
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
		// Sprint 15d : plafonds de retrait des particuliers.
		var req struct {
			NodeID    string  `json:"node_id"`
			Provider  string  `json:"provider"`
			AmountEur float64 `json:"amount_eur"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.NodeID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if uid := registry.NodeOwner(req.NodeID); uid != "" {
			if u := accounts.UserByID(uid); u != nil && u.isParticulier() {
				last30d, total := 0.0, 0.0
				for _, wd := range wallet.Withdrawals("") {
					if registry.NodeOwner(wd.NodeID) == uid {
						total += wd.AmountEur
						if time.Since(wd.CreatedAt) < 30*24*time.Hour {
							last30d += wd.AmountEur
						}
					}
				}
				if !WithdrawAllowed(u, last30d+req.AmountEur, total+req.AmountEur) {
					http.Error(w, "plafond de retrait atteint pour un compte particulier (100 EUR / 30 jours, 500 EUR cumules)", http.StatusForbidden)
					return
				}
			}
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
	// Sprint 23 : GET /v1/jobs liste les jobs de l'org, POST soumet.
	http.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			v1JobsList(orgs, chunker, billing).ServeHTTP(w, r)
			return
		}
		v1Submit(orgs, assets, chunker, billing, accounts).ServeHTTP(w, r)
	})
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
	// Sprint 26 : telechargement d'un fichier de resultat par le client.
	http.HandleFunc("/v1/results/", downloadResult(orgs, chunker, store))

	// Sprint 26 : purge des fichiers de resultat expires (30 jours).
	go purgeResultsLoop(store)

	// LAI-28 : object store des assets clients (auth Bearer).
	http.HandleFunc("/v1/assets", v1UploadAsset(orgs, assets))
	http.HandleFunc("/v1/assets/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/node-download"):
			// Sprint 27 : telechargement d'un asset par le noeud qui
			// execute le chunk (jeton de noeud, chunk concerne).
			nodeAuth.withNodeToken(func(w http.ResponseWriter, r2 *http.Request, _ string) {
				v1NodeDownloadAsset(assets, chunker).ServeHTTP(w, r2)
			}).ServeHTTP(w, r)
		case strings.HasSuffix(r.URL.Path, "/download"):
			v1DownloadAsset(orgs, assets).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// Sprint 13 - Console B2B + billing (LAI-31/32).
	// Console client : formulaire de soumission + suivi live.
	http.HandleFunc("/console", consolePage)
	// Solde de credits et rapport d'usage du client authentifie.
	http.HandleFunc("/v1/balance", v1Balance(orgs, billing))
	http.HandleFunc("/v1/usage", v1Usage(orgs, billing))
	// Admin : vue billing, rechargement, remboursement.
	http.HandleFunc("/admin/billing", adminBilling(billing))
	http.HandleFunc("/admin/billing/topup", func(w http.ResponseWriter, r *http.Request) {
		// Sprint 15d : un compte particulier (ou societe non verifiee)
		// ne peut pas depasser maxParticulierTopupEur de solde.
		// Fix 5 nov. 2026 : le wrapper relisait le corps deja consomme
		// par l'ancien handler -> toujours "invalid". Le rechargement
		// est desormais fait entierement ici.
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			OrgID     string  `json:"org_id"`
			AmountEur float64 `json:"amount_eur"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if u := accounts.UserByOrg(req.OrgID); u != nil &&
			!TopupAllowed(u, billing.Balance(req.OrgID)+req.AmountEur) {
			http.Error(w, "plafond de credits atteint pour un compte particulier (passez le compte en societe verifiee)", http.StatusForbidden)
			return
		}
		e, err := billing.TopUp(req.OrgID, req.AmountEur)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dashLog("admin", fmt.Sprintf("rechargement de %.2f EUR pour %s par l'admin", req.AmountEur, req.OrgID))
		writeJSON(w, e)
	})
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