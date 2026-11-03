package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Sprint 8 : persistance PostgreSQL (mode memoire si BDD absente).
	store := NewStore()
	registry := NewRegistry(store)
	hub := NewHub(registry)
	pricing := NewPricing(store)
	chunker := NewChunker(registry, pricing, store)
	store.LoadAll(registry, pricing, chunker)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	http.HandleFunc("/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, registry.OnlineNodes())
	})
	http.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			NodeID      string `json:"node_id"`
			GPUModel    string `json:"gpu_model"`
			GPUCategory string `json:"gpu_category"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil || msg.NodeID == "" {
			http.Error(w, "invalid register", http.StatusBadRequest)
			return
		}
		writeJSON(w, registry.Register(msg.NodeID, msg.GPUModel, msg.GPUCategory))
	})
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		nodeWS(registry, hub, w, r)
	})
	http.HandleFunc("/jobs", submitJob(chunker))
	http.HandleFunc("/chunks", nextChunk(chunker))
	http.HandleFunc("/chunk_result", completeChunk(chunker))

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

	log.Printf("orchestrator listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
