package main

import (
"fmt"
"net/http"
"os"
)

func main() {
port := os.Getenv("PORT")
if port == "" {
port = "8080"
}
http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
fmt.Fprintln(w, "ok")
})
fmt.Println("orchestrator listening on :" + port)
if err := http.ListenAndServe(":"+port, nil); err != nil {
panic(err)
}
}
