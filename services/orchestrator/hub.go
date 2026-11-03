package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type Message struct {
	Type               string  `json:"type"`
	NodeID             string  `json:"node_id,omitempty"`
	GPUModel           string  `json:"gpu_model,omitempty"`
	GPUCategory        string  `json:"gpu_category,omitempty"`
	Zone               string  `json:"zone,omitempty"`
	GPULoad            float64 `json:"gpu_load,omitempty"`
	Intensity          float64 `json:"intensity,omitempty"`
	MachineFingerprint string  `json:"machine_fingerprint,omitempty"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	registry *Registry
}

func NewHub(r *Registry) *Hub { return &Hub{registry: r} }

func nodeWS(reg *Registry, hub *Hub, w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade: %v", err)
		return
	}
	defer ws.Close()

	ws.SetReadDeadline(time.Now().Add(heartbeatTimeout))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(heartbeatTimeout))
		return nil
	})

	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				ws.Close()
				return
			}
		}
	}()

	for {
		var msg Message
		if err := ws.ReadJSON(&msg); err != nil {
			log.Printf("node disconnected: %v", err)
			return
		}
		switch msg.Type {
		case "register":
			// LAI-44 : l'empreinte machine accompagne le register
			// pour la detection anti-Sybil.
			n := reg.Register(msg.NodeID, msg.GPUModel, msg.GPUCategory,
				msg.Zone, msg.MachineFingerprint)
			log.Printf("node registered: %s (%s/%s, zone %s, trust %.0f)",
				n.ID, n.GPUModel, n.GPUCategory, n.Zone, n.TrustScore)
		case "heartbeat":
			if !reg.Heartbeat(msg.NodeID) {
				log.Printf("heartbeat d'un noeud inconnu: %s", msg.NodeID)
			}
		}
	}
}
