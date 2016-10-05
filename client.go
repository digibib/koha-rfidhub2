package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	rfidconn net.Conn
	rfid     *RFIDManager
	readBuf  []byte
	fromKoha chan Message
	fromRFID chan RFIDResp
	quit     chan bool
}

// Run the state-machine of the client
func (c *Client) Run(cfg Config) {
	go c.initRFID(cfg.RFIDPort)
	for {
		select {
		case msg := <-c.fromKoha:
			switch msg.Action {
			case "CHECKIN":
				c.state = RFIDCheckinWaitForBegOK
			case "END":
				c.state = RFIDWaitForEndOK
			case "ITEM-INFO":
			case "WRITE":
			case "CHECKOUT":
			case "RETRY-ALARM-ON":
			case "RETRY-ALARM-OFF":
			}
		//case msg := <-c.fromRFID:
		case <-c.quit:
			c.write(websocket.CloseMessage, []byte{})
			return
		}
	}
}

func (c *Client) initRFID(port string) {
	var err error
	c.rfidconn, err = net.Dial("tcp", net.JoinHostPort(c.IP, port))
	if err != nil {
		log.Printf("ERR [%s] RFID server tcp connect: %v", c.IP, err)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
		c.quit <- true
		return
	}
	// Init the RFID-unit with version command
	var initError string
	req := c.rfid.GenRequest(RFIDReq{Cmd: cmdInitVersion})
	_, err = c.rfidconn.Write(req)
	if err != nil {
		initError = err.Error()
	}
	log.Printf("-> [%s] %q", c.IP, string(req))

	r := bufio.NewReader(c.rfidconn)
	n, err := r.Read(c.readBuf)
	if err != nil {
		initError = err.Error()
	}
	resp, err := c.rfid.ParseResponse(c.readBuf[:n])
	if err != nil {
		initError = err.Error()
	}
	log.Printf("<- [%s] %q", c.IP, string(c.readBuf[:n]))

	if initError == "" && !resp.OK {
		initError = "RFID-unit responded with NOK"
	}

	if initError != "" {
		log.Printf("ERR [%s] RFID initialization: %s", c.IP, initError)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: initError})
		c.quit <- true
		return
	}

	log.Printf("[%s] RIFD connected & initialized", c.IP)

	go c.readFromRFID(r)

	// Notify UI of success:
	c.sendToKoha(Message{Action: "CONNECT"})
}

func (c *Client) readFromKoha() {
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
		log.Printf("ERR sendToKoha json.Marshal(msg): %v", err)
		return
	}
	w.Write(b)

	if err := w.Close(); err != nil {
		return
	}
}

func (c *Client) readFromRFID(r io.Reader) {
	for {
		n, err := r.Read(c.readBuf)
		if err != nil {
			log.Printf("[%v] RFID server tcp read failed: %v", c.IP, err)
			c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
			c.quit <- true
			break
		}
		log.Printf("<- [%v] %q", c.IP, string(c.readBuf[:n]))

		resp, err := c.rfid.ParseResponse(c.readBuf[:n])
		if err != nil {
			log.Printf("ERR [%v] %v", c.IP, err)
			c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
			c.quit <- true // TODO really?
			break
		}
		c.fromRFID <- resp
	}
}

func (c *Client) sendToRFID(req RFIDReq) {
	b := c.rfid.GenRequest(req)
	_, err := c.rfidconn.Write(b)
	if err != nil {
		log.Printf("ERR [%v] %v", c.IP, err)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
		c.quit <- true
		return
	}
	log.Printf("-> [%v] %q", c.IP, string(b))
}
