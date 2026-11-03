package main

import (
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
