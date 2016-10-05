package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"text/template"

	"github.com/gorilla/websocket"
)

type Config struct {
	RFIDPort  string // Port which RFID-unit is listening on
	HTTPPort  string // Listening Port of the HTTP and WebSocket server
	SIPServer string // Adress of the SIP-server

	// Credentials for SIP user to use in rfid-hub
	SIPUser string
	SIPPass string
	SIPDept string

	// Number of SIP-connections to keep in the pool
	NumSIPConnections int

	LogSIPMessages bool
}

// global variables
var (
	config = Config{
		RFIDPort:          "6005",
		HTTPPort:          "8899",
		SIPServer:         "sip_proxy:9999",
		SIPUser:           "autouser",
		SIPPass:           "autopass",
		NumSIPConnections: 10,
	}

	// TODO remove:
	addr         = flag.String("addr", ":8899", "http service address")
	homeTemplate = template.Must(template.ParseFiles("test.html"))
)

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", 404)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	homeTemplate.Execute(w, r.Host)
}

func init() {
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
	if os.Getenv("SIP_CONNS") != "" {
		n, err := strconv.Atoi(os.Getenv("SIP_CONNS"))
		if err != nil {
			log.Fatal(err)
		}
		config.NumSIPConnections = n
	}
}

func main() {
	flag.Parse()
	hub := newHub()
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatal("http.ListenAndServe: ", err)
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// serveWs handles websocket requests from the peer.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		log.Println("ERR cannot get remote IP address: %v", err)
		return
	}
	client := &Client{
		IP:       ip,
		hub:      hub,
		conn:     conn,
		fromKoha: make(chan Message),
		fromRFID: make(chan RFIDResp),
		quit:     make(chan bool, 5),
		readBuf:  make([]byte, 1024),
		rfid:     newRFIDManager(),
	}
	hub.Connect(client)
	go client.Run(config)
	client.readFromKoha()
}
