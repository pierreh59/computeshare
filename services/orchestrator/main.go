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
registry := NewRegistry()
hub := NewHub(registry)
chunker := NewChunker(registry)

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

log.Printf("orchestrator listening on :%s", port)
if err := http.ListenAndServe(":"+port, nil); err != nil {
log.Fatal(err)
}
}
