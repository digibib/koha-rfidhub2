package main

import (
	"sync"

	"github.com/gorilla/websocket"
)

// Hub maintains the set of connected clients, to make sure we only have one per IP.
type Hub struct {
	mu          sync.Mutex         // Protects the following:
	clients     map[*Client]bool   // Connected clients
	clientsByIP map[string]*Client // Connected clients keyed by IP-address
}

func newHub() *Hub {
	return &Hub{
		clients:     make(map[*Client]bool),
		clientsByIP: make(map[string]*Client),
	}
}

func (h *Hub) Connect(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.clientsByIP[c.IP]; ok {
		// There is allready a connection from the same IP, disconnect it.
		delete(h.clients, old)
		close(old.quit)
		old.write(websocket.CloseMessage, []byte{})
	}
	h.clients[c] = true
	h.clientsByIP[c.IP] = c
}

func (h *Hub) Disconnect(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.quit)
		c.write(websocket.CloseMessage, []byte{})
		delete(h.clientsByIP, c.IP)
	}
}
