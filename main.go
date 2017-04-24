package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

type Config struct {
	RFIDPort  string // Port which RFID-unit is listening on
	HTTPPort  string // Listening Port of the HTTP and WebSocket server
	SIPServer string // Address of the SIP-server

	// Credentials for SIP user to use in rfid-hub
	SIPUser    string
	SIPPass    string
	SIPDept    string
	SIPMaxConn int

	RFIDTimeout time.Duration

	WSProxy bool

	LogSIPMessages bool
}

// global variables
var (
	config = Config{
		RFIDPort:       "6005",
		HTTPPort:       "8899",
		SIPServer:      "sip_proxy:9999",
		SIPUser:        "autouser",
		SIPPass:        "autopass",
		SIPMaxConn:     5,
		LogSIPMessages: true,
		RFIDTimeout:    15 * time.Minute,
		WSProxy:        true,
	}

	hub *Hub
)

func init() {
	// TODO move these to command line flags
	if os.Getenv("TCP_PORT") != "" {
		config.RFIDPort = os.Getenv("TCP_PORT")
	}
	if os.Getenv("HTTP_PORT") != "" {
		config.HTTPPort = os.Getenv("HTTP_PORT")
	}
	if os.Getenv("SIP_SERVER") != "" {
		config.SIPServer = os.Getenv("SIP_SERVER")
	}
	if os.Getenv("SIP_USER") != "" {
		config.SIPUser = os.Getenv("SIP_USER")
	}
	if os.Getenv("SIP_PASS") != "" {
		config.SIPPass = os.Getenv("SIP_PASS")
	}

	// TODO move somewhere else
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
}

func main() {
	flag.DurationVar(&config.RFIDTimeout, "rfid-timeout", 15*time.Minute, "RFID-timeout in Koha UI")
	flag.IntVar(&config.SIPMaxConn, "sip-maxconn", 5, "Max size of SIP connection pool")
	flag.BoolVar(&config.WSProxy, "ws-proxy", true, "WS goes through proxy, find client IP in request header")

	flag.Parse()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	hub = newHub(config)
	defer hub.Close()

	log.Fatal(http.ListenAndServe(":"+config.HTTPPort, nil))
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// serveWs handles websocket requests from the peer.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	var ip string
	if hub.config.WSProxy {
		ip = r.Header.Get("X-Forwarded-For")
	} else {
		ip, _, err = net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			log.Printf("ERR cannot get remote IP address: %v", err)
			return
		}
	}
	client := &Client{
		IP:             ip,
		hub:            hub,
		conn:           conn,
		fromKoha:       make(chan Message),
		fromRFID:       make(chan RFIDResp),
		quit:           make(chan bool, 5),
		rfid:           newRFIDManager(),
		items:          make(map[string]Message),
		failedAlarmOn:  make(map[string]string),
		failedAlarmOff: make(map[string]string),
	}
	hub.Connect(client)
	rfid, ok := client.initRFID(hub.config.RFIDPort)
	if !ok {
		hub.Disconnect(client)
		return
	}
	go client.readFromRFID(rfid)
	go client.Run(hub.config)
	client.readFromKoha()
}
