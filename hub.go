package main

import "sync"

// Hub maintains the set of connected clients, to make sure we only have one per IP.
type Hub struct {
	mu          sync.Mutex         // Protects the following:
	clients     map[*Client]bool   // Connected clients
	clientsByIP map[string]*Client // Connected clients keyed by IP-address
	config      Config
	sipPool     *pool
}

func newHub(cfg Config) *Hub {
	return &Hub{
		clients:     make(map[*Client]bool),
		clientsByIP: make(map[string]*Client),
		config:      cfg,
		sipPool:     newPool(cfg.SIPMaxConn, initSIPConn(cfg)),
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.quit <- true
	}
}

func (h *Hub) Connect(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.clientsByIP[c.IP]; ok {
		// There is allready a connection from the same IP, disconnect it.
		delete(h.clients, old)
		old.quit <- true
	}
	h.clients[c] = true
	h.clientsByIP[c.IP] = c
}

func (h *Hub) Disconnect(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		c.quit <- true
		delete(h.clientsByIP, c.IP)
	}
}
