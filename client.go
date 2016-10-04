package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 5 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// Client represents a connected Koha intra UI client with RFID-capabilities.
type Client struct {
	state    RFIDState
	IP       string
	hub      *Hub
	conn     *websocket.Conn
	fromKoha chan Message
	quit     chan bool
}

// Run the state-machine of the client
func (c *Client) Run() {
	for {
		select {
		case msg := <-c.fromKoha:
			fmt.Printf("Got from Koha: %v", msg)
		//case msg := <-c.fromRFID:
		case <-c.quit:
			return
		}
	}
}

// reader pumps messages from the UI websocket to the client.
func (c *Client) reader() {
	defer func() {
		c.hub.Disconnect(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		fmt.Printf("from UI: %s\n", string(message))
	}
}

// write writes a message with the given message type and payload.
func (c *Client) write(mt int, payload []byte) error {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(mt, payload)
}

func (c *Client) sendToKoha(msg Message) {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	w, err := c.conn.NextWriter(websocket.TextMessage)
	if err != nil {
		return
	}
	b, err := json.Marshal(msg)
	if err != nil {
		log.Printf("sendToKoha json.Marshal(msg): %v", err)
		return
	}
	w.Write(b)

	if err := w.Close(); err != nil {
		return
	}
}
