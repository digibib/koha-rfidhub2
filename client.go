package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 5 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// Client represents a connected Koha intra UI client with RFID-capabilities.
type Client struct {
	state          RFIDState
	branch         string
	patron         string
	current        Message
	items          map[string]Message // Keep items around for retries, keyed by barcode TODO drop Message, store only Item
	failedAlarmOn  map[string]string  // map[Barcode]Tag
	failedAlarmOff map[string]string  // map[Barcode]Tag
	IP             string
	hub            *Hub
	wlock          sync.Mutex
	conn           *websocket.Conn
	rfidLock       sync.Mutex
	rfidconn       net.Conn
	rfid           *RFIDManager
	fromKoha       chan Message
	fromRFID       chan RFIDResp
	quit           chan bool
}

// Run the state-machine of the client
func (c *Client) Run(cfg Config) {
	for {
		select {
		case msg := <-c.fromKoha:
			switch msg.Action {
			case "CHECKIN":
				c.state = RFIDCheckinWaitForBegOK
				c.rfid.Reset()
				c.branch = msg.Branch
				c.sendToRFID(RFIDReq{Cmd: cmdBeginScan})
			case "END":
				c.state = RFIDWaitForEndOK
				c.sendToRFID(RFIDReq{Cmd: cmdEndScan})
			case "ITEM-INFO":
				var err error
				c.current, err = DoSIPCall(c.hub.config, c.hub.sipPool, sipFormMsgItemStatus(msg.Item.Barcode), itemStatusParse, c.IP)
				if err != nil {
					log.Printf("ER [%s] SIP call: %v", c.IP, err)
					c.sendToKoha(Message{Action: "ITEM-INFO", SIPError: true, ErrorMessage: err.Error()})
					c.quit <- true // really?
					break
				}
				c.state = RFIDWaitForTagCount
				c.rfid.Reset()
				c.sendToRFID(RFIDReq{Cmd: cmdTagCount})
			case "WRITE":
				c.state = RFIDPreWriteStep1
				c.current.Action = "WRITE"
				c.current.Item.NumTags = msg.Item.NumTags
				c.rfid.Reset()
				c.sendToRFID(RFIDReq{Cmd: cmdSLPLBN})
			case "CHECKOUT":
				if msg.Patron == "" {
					c.sendToKoha(Message{Action: "CHECKOUT",
						UserError: true, ErrorMessage: "Patron not supplied"})
					c.state = RFIDIdle
					break
				}
				c.state = RFIDCheckoutWaitForBegOK
				c.patron = msg.Patron
				c.branch = msg.Branch
				c.rfid.Reset()
				c.sendToRFID(RFIDReq{Cmd: cmdBeginScan})
			case "RETRY-ALARM-ON":
				c.state = RFIDWaitForRetryAlarmOn
				for k, v := range c.failedAlarmOn {
					c.current = c.items[k]
					c.current.Item.Transfer = ""
					c.sendToRFID(RFIDReq{Cmd: cmdRetryAlarmOn, Data: []byte(v)})
					break // Remaining will be triggered in case RFIDWaitForRetryAlarmOn
				}
			case "RETRY-ALARM-OFF":
				c.state = RFIDWaitForRetryAlarmOff
				for k, v := range c.failedAlarmOff {
					c.current = c.items[k]
					c.sendToRFID(RFIDReq{Cmd: cmdRetryAlarmOff, Data: []byte(v)})
					break // Remaining will be triggered in case RFIDWaitForRetryAlarmOff
				}
				// TODO default case -> ERROR
			}
		case resp := <-c.fromRFID:
			switch c.state {
			case RFIDCheckinWaitForBegOK:
				if !resp.OK {
					log.Printf("ERR: [%v] RFID failed to start scanning", c.IP)
					c.sendToKoha(Message{Action: "CONNECT", RFIDError: true})
					c.quit <- true
					break
				}
				c.state = RFIDCheckin
			case RFIDWaitForCheckinAlarmLeave:
				c.state = RFIDCheckin
				c.current.Item.Date = ""
				c.sendToKoha(c.current)
			case RFIDWaitForCheckinAlarmOn:
				c.state = RFIDCheckin
				if !resp.OK {
					c.current.Item.AlarmOnFailed = true
					c.current.Item.Status = "Feil: fikk ikke skrudd på alarm."
				} else {
					delete(c.failedAlarmOn, c.current.Item.Barcode)
					c.current.Item.AlarmOnFailed = false
					c.current.Item.Status = ""
				}
				// Discard branchcode if issuing branch is the same as target branch
				if c.branch == c.current.Item.Transfer {
					c.current.Item.Transfer = ""
				}
				c.sendToKoha(c.current)
			case RFIDWaitForRetryAlarmOn:
				if !resp.OK {
					c.current.Item.AlarmOnFailed = true
					c.current.Item.Status = "Feil: fikk ikke skrudd på alarm."
				} else {
					delete(c.failedAlarmOn, c.current.Item.Barcode)
					c.current.Item.Status = ""
					c.current.Item.AlarmOnFailed = false
				}
				c.sendToKoha(c.current)

				if len(c.failedAlarmOn) > 0 {
					for k, v := range c.failedAlarmOn {
						c.current = c.items[k]
						c.current.Item.Transfer = ""
						c.state = RFIDWaitForRetryAlarmOn
						c.sendToRFID(RFIDReq{Cmd: cmdRetryAlarmOn, Data: []byte(v)})
						break
					}
				} else {
					c.state = RFIDCheckin
				}
			case RFIDCheckin:
				var err error
				if !resp.OK {
					// Not OK on checkin means missing tags

					// Get item info from SIP, in order to have a title to display
					// Don't bother calling SIP if this is already the current item
					if barcodeFromTag(resp.Tag) != c.current.Item.Barcode {
						c.current, err = DoSIPCall(c.hub.config, c.hub.sipPool, sipFormMsgItemStatus(resp.Tag), itemStatusParse, c.IP)
						if err != nil {
							log.Printf("ER [%s] SIP: %v", c.IP, err)
							c.sendToKoha(Message{Action: "CONNECT", SIPError: true, ErrorMessage: err.Error()})
							c.quit <- true
							break
						}
					}
					c.current.Action = "CHECKIN"
					c.items[barcodeFromTag(resp.Tag)] = c.current
					c.sendToRFID(RFIDReq{Cmd: cmdAlarmLeave})
					c.state = RFIDWaitForCheckinAlarmLeave
					break
				} else {
					// Proceed with checkin transaction
					c.current, err = DoSIPCall(c.hub.config, c.hub.sipPool, sipFormMsgCheckin(c.branch, resp.Tag), checkinParse, c.IP)
					if err != nil {
						log.Printf("ER [%s] SIP call failed: %v", c.IP, err)
						c.sendToKoha(Message{Action: "CHECKIN", SIPError: true, ErrorMessage: err.Error()})
						// TODO send cmdAlarmLeave to RFID?
						break
					}
					if c.current.Item.Unknown || c.current.Item.TransactionFailed {
						c.sendToRFID(RFIDReq{Cmd: cmdAlarmLeave})
						c.state = RFIDWaitForCheckinAlarmLeave
					} else {
						c.items[barcodeFromTag(resp.Tag)] = c.current
						c.failedAlarmOn[barcodeFromTag(resp.Tag)] = resp.Tag // Store tag id for potential retry
						c.sendToRFID(RFIDReq{Cmd: cmdAlarmOn})
						c.state = RFIDWaitForCheckinAlarmOn
					}
				}
			case RFIDCheckout:
				var err error
				if !resp.OK {
					// Missing tags case
					// TODO test this case

					// Get status of item, to have title to display on screen,
					// Don't bother calling SIP if this is already the current item
					if barcodeFromTag(resp.Tag) != c.current.Item.Barcode {
						c.current, err = DoSIPCall(c.hub.config, c.hub.sipPool, sipFormMsgItemStatus(resp.Tag), itemStatusParse, c.IP)
						if err != nil {
							log.Printf("ER [%s] SIP call failed: %v", c.IP, err)
							c.sendToKoha(Message{Action: "CHECKOUT", SIPError: true, ErrorMessage: err.Error()})
							// c.quit <- true // really?
							break
						}
					}
					c.current.Action = "CHECKOUT"
					c.items[barcodeFromTag(resp.Tag)] = c.current
					c.sendToRFID(RFIDReq{Cmd: cmdAlarmLeave})
					c.state = RFIDWaitForCheckoutAlarmLeave
				} else {
					// proced with checkout transaction
					c.current, err = DoSIPCall(c.hub.config, c.hub.sipPool, sipFormMsgCheckout(c.branch, c.patron, resp.Tag), checkoutParse, c.IP)
					if err != nil {
						log.Printf("ER [%s] SIP call failed: %v", c.IP, err)
						c.sendToKoha(Message{Action: "CHECKOUT", SIPError: true, ErrorMessage: err.Error()})
						// c.quit <- true // really?
						break
					}
					c.current.Action = "CHECKOUT"
					if c.current.Item.Unknown || c.current.Item.TransactionFailed {
						c.sendToRFID(RFIDReq{Cmd: cmdAlarmLeave})
						c.state = RFIDWaitForCheckoutAlarmLeave
						break
					} else {
						c.items[barcodeFromTag(resp.Tag)] = c.current
						c.failedAlarmOff[barcodeFromTag(resp.Tag)] = resp.Tag // Store tag id for potential retry
						c.sendToRFID(RFIDReq{Cmd: cmdAlarmOff})
						c.state = RFIDWaitForCheckoutAlarmOff
					}
				}
			case RFIDCheckoutWaitForBegOK:
				if !resp.OK {
					log.Printf("ER [%v] RFID failed to start scanning, shutting down.", c.IP)
					c.sendToKoha(Message{Action: "CHECKOUT", RFIDError: true})
					c.quit <- true // really?
					break
				}
				c.state = RFIDCheckout
			case RFIDWaitForCheckoutAlarmOff:
				c.state = RFIDCheckout
				if !resp.OK {
					// TODO unit-test for this
					c.current.Item.AlarmOffFailed = true
					c.current.Item.Status = "Feil: fikk ikke skrudd av alarm."
				} else {
					delete(c.failedAlarmOff, c.current.Item.Barcode)
					c.current.Item.Status = ""
					c.current.Item.AlarmOffFailed = false
				}
				c.sendToKoha(c.current)
			case RFIDWaitForCheckoutAlarmLeave:
				if !resp.OK {
					// I can't imagine the RFID-reader fails to leave the
					// alarm in it current state. In any case, we continue
					log.Printf("ER [%v] RFID reader failed to leave alarm in current state", c.IP)
				}
				c.state = RFIDCheckout
				c.sendToKoha(c.current)
			case RFIDWaitForRetryAlarmOff:
				if !resp.OK {
					c.current.Item.AlarmOffFailed = true
					c.current.Item.Status = "Feil: fikk ikke skrudd av alarm."
				} else {
					delete(c.failedAlarmOff, c.current.Item.Barcode)
					c.current.Item.Status = ""
					c.current.Item.AlarmOffFailed = false
				}
				c.sendToKoha(c.current)

				if len(c.failedAlarmOff) > 0 {
					for k, v := range c.failedAlarmOff {
						c.current = c.items[k]
						c.state = RFIDWaitForCheckoutAlarmOff
						c.sendToRFID(RFIDReq{Cmd: cmdRetryAlarmOff, Data: []byte(v)})
						break
					}
				} else {
					c.state = RFIDCheckin
				}
			case RFIDWaitForTagCount:
				c.current.Item.TransactionFailed = !resp.OK
				c.state = RFIDIdle
				c.current.Action = "ITEM-INFO"
				c.current.Item.NumTags = resp.TagCount
				c.sendToKoha(c.current)
			case RFIDPreWriteStep1:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep2
				c.sendToRFID(RFIDReq{Cmd: cmdSLPLBC})
			case RFIDPreWriteStep2:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep3
				c.sendToRFID(RFIDReq{Cmd: cmdSLPDTM})
			case RFIDPreWriteStep3:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep4
				c.sendToRFID(RFIDReq{Cmd: cmdSLPSSB})
			case RFIDPreWriteStep4:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep5
				c.sendToRFID(RFIDReq{Cmd: cmdSLPCRD})
			case RFIDPreWriteStep5:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep6
				c.sendToRFID(RFIDReq{Cmd: cmdSLPWTM})
			case RFIDPreWriteStep6:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep7
				c.sendToRFID(RFIDReq{Cmd: cmdSLPRSS})
			case RFIDPreWriteStep7:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDPreWriteStep8
				c.sendToRFID(RFIDReq{Cmd: cmdTagCount})
			case RFIDPreWriteStep8:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				if resp.TagCount != c.current.Item.NumTags {
					// Mismatch between number of tags on the RFID-reader and
					// expected number assigned in the UI.
					errMsg := fmt.Sprintf("forventet %d brikke(r), men fant %d.",
						c.current.Item.NumTags, resp.TagCount)
					c.current.Item.Status = errMsg
					c.current.Item.TagCountFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				} else {
					c.current.Item.TagCountFailed = false
				}
				c.state = RFIDWriting
				c.sendToRFID(
					RFIDReq{Cmd: cmdWrite,
						Data:     []byte(c.current.Item.Barcode),
						TagCount: c.current.Item.NumTags})
			case RFIDWriting:
				if !resp.OK {
					c.current.Item.WriteFailed = true
					c.sendToKoha(c.current)
					c.state = RFIDIdle
					break
				}
				c.state = RFIDIdle
				c.current.Item.WriteFailed = false
				c.current.Item.Status = "OK, preget"
				c.sendToKoha(c.current)
				// TODO default case -> ERROR
			}
		case <-c.quit:
			//c.sendToRFID(RFIDReq{Cmd: cmdEndScan})
			c.wlock.Lock()
			c.write(websocket.CloseMessage, []byte{})
			c.wlock.Unlock()
			return
		}
	}
}

func (c *Client) initRFID(port string) (*bufio.Reader, bool) {
	var err error
	c.rfidLock.Lock()
	defer c.rfidLock.Unlock()
	c.rfidconn, err = net.Dial("tcp", net.JoinHostPort(c.IP, port))
	if err != nil {
		log.Printf("ER [%s] RFID server tcp connect: %v", c.IP, err)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
		return nil, false
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
	b, err := r.ReadBytes('\r')
	if err != nil {
		initError = err.Error()
	}
	resp, err := c.rfid.ParseResponse(b)
	if err != nil {
		initError = err.Error()
	}
	log.Printf("<- [%s] %q", c.IP, string(b))

	if initError == "" && !resp.OK {
		initError = "RFID-unit responded with NOK"
	}

	if initError != "" {
		log.Printf("ER [%s] RFID initialization: %s", c.IP, initError)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: initError})
		return nil, false
	}

	log.Printf("OK [%s] RIFD connected & initialized", c.IP)

	// Notify UI of success:
	c.sendToKoha(Message{Action: "CONNECT"})
	return r, true
}

func (c *Client) readFromKoha() {
	defer func() {
		c.hub.Disconnect(c)
		c.conn.Close()
		c.rfidLock.Lock()
		if c.rfidconn != nil {
			c.rfidconn.Close()
		}
		c.rfidLock.Unlock()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(c.hub.config.RFIDTimeout))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(c.hub.config.RFIDTimeout)); return nil })
	for {
		_, jsonMsg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var msg Message
		if err := json.Unmarshal(jsonMsg, &msg); err != nil {
			log.Printf("ER [%s] unmarshal message: %v", c.IP, err)
			c.sendToKoha(Message{Action: "CONNECT", UserError: true, ErrorMessage: err.Error()})
			continue
		}
		c.fromKoha <- msg
	}
}

// write writes a message with the given message type and payload.
func (c *Client) write(mt int, payload []byte) error {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(mt, payload)
}

func (c *Client) sendToKoha(msg Message) {
	c.wlock.Lock()
	defer c.wlock.Unlock()
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

func (c *Client) readFromRFID(r *bufio.Reader) {
	for {
		b, err := r.ReadBytes('\r')
		if err != nil && len(b) == 0 {
			log.Printf("ER [%v] RFID server tcp read failed: %v", c.IP, err)
			c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
			c.quit <- true
			break
		}
		log.Printf("<- [%v] %q", c.IP, string(b))

		resp, err := c.rfid.ParseResponse(b)
		if err != nil {
			log.Printf("ER [%v] %v", c.IP, err)
			c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
			c.quit <- true // TODO really?
			break
		}
		select {
		case c.fromRFID <- resp:
		case <-time.After(time.Second * 3):
			return
		}
	}
}

func (c *Client) sendToRFID(req RFIDReq) {
	b := c.rfid.GenRequest(req)
	c.rfidLock.Lock()
	defer c.rfidLock.Unlock()
	if c.rfidconn == nil {
		log.Println("?? RFID conn gone TODO investigate")
		return
	}
	_, err := c.rfidconn.Write(b)
	if err != nil {
		log.Printf("ER [%v] %v", c.IP, err)
		c.sendToKoha(Message{Action: "CONNECT", RFIDError: true, ErrorMessage: err.Error()})
		c.quit <- true
		return
	}
	log.Printf("-> [%v] %q", c.IP, string(b))
}

func barcodeFromTag(tag string) string {
	var barcode string
	id := strings.Split(tag, ":")
	if len(id) == 3 && id[2] == "02030000" {
		barcode = strings.TrimPrefix(id[0], "10")
	} else {
		barcode = id[0]
	}
	return barcode
}
