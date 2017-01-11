package main

import (
	"bufio"
	"net"
	"sync"
	"testing"
	"time"
)

type SIPTestServer struct {
	sync.RWMutex
	l       net.Listener
	echo    []byte
	failing bool
}

func newSIPTestServer() *SIPTestServer {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	s := SIPTestServer{l: l}
	go s.run()
	return &s
}

func (s *SIPTestServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	auth := false
	for {
		_, _ = r.ReadBytes('\r')
		msg := s.echo
		if !auth {
			msg = []byte("941\r")
		}
		_, err := conn.Write(msg)
		if err != nil {
			break
		}
		if auth {
			return
		}
		auth = true
	}
}
func (s *SIPTestServer) run() {
	for {
		conn, err := s.l.Accept()
		if err != nil {
			return
		}

		s.RLock()
		if s.failing {
			conn.Close()
			s.RUnlock()
			return
		}
		s.RUnlock()
		go s.handle(conn)
	}
}

func (s *SIPTestServer) Respond(msg string) {
	s.Lock()
	defer s.Unlock()
	s.echo = []byte(msg)
}
func (s *SIPTestServer) Addr() string {
	s.RLock()
	defer s.RUnlock()
	return s.l.Addr().String()
}
func (s *SIPTestServer) Close() { s.l.Close() }
func (s *SIPTestServer) Failing() *SIPTestServer {
	s.Lock()
	defer s.Unlock()
	s.failing = true
	return s
}

func TestSIPCheckin(t *testing.T) {
	srv := newSIPTestServer()
	defer srv.Close()

	initFn := initSIPConn(Config{SIPServer: srv.Addr(), RFIDTimeout: 1 * time.Second})

	srv.Respond("101YNN20140124    093621AOHUTL|AB03011143299001|AQhvmu|AJ316 salmer og sanger|AA1|CS783.4|\r")

	res, err := DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgCheckin("HUTL", "03011143299001"), checkinParse)
	if err != nil {
		t.Fatal(err)
	}
	if res.Item.TransactionFailed {
		t.Errorf("res.Item.TransactionFailed == true; want false")
	}
	if want := "316 salmer og sanger"; res.Item.Label != want {
		t.Errorf("res.Item.Label == %q; want %q", res.Item.Label, want)
	}

	if want := "24/01/2014"; res.Item.Date != want {
		t.Errorf("res.Item.Date == %q; want %q", res.Item.Date, want)
	}

	srv.Respond("100NUY20140128    114702AO|AB234567890|CV99|AFItem not checked out|\r")
	res, err = DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgCheckin("HUTL", "234567890"), checkinParse)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Item.TransactionFailed {
		t.Errorf("res.Item.TransactionFailed == false; want true")
	}
	if want := "eksemplaret finnes ikke i basen"; res.Item.Status != want {
		t.Errorf("res.Item.Status == %q; want %q", res.Item.Status, want)
	}

	srv.Respond("100YNY20140511    092216AOGRY|AB03010013753001|AQhutl|AJHeksenes historie|CS272 And|CTfroa|CY11|DAåsen|CV02|AFItem not checked out|\r")
	res, err = DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgCheckin("hutl", "03010013753001"), checkinParse)
	if err != nil {
		t.Fatal(err)
	}
	if want := "froa"; res.Item.Transfer != want {
		t.Errorf("res.Item.Transfer == %q; want %q", res.Item.Transfer, want)
	}
}

func TestSIPCheckout(t *testing.T) {
	srv := newSIPTestServer()
	defer srv.Close()

	initFn := initSIPConn(Config{SIPServer: srv.Addr(), RFIDTimeout: 1 * time.Second})

	srv.Respond("121NNY20140124    110740AOHUTL|AA2|AB03011174511003|AJKrutt-Kim|AH20140221    235900|\r")
	res, err := DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgCheckout("HUTL", "2", "03011174511003"), checkoutParse)
	if err != nil {
		t.Fatal(err)
	}
	if res.Item.TransactionFailed {
		t.Errorf("res.Item.TransactionFailed == true; want false")
	}
	if want := "Krutt-Kim"; res.Item.Label != want {
		t.Errorf("res.Item.Label == %q; want %q", res.Item.Label, want)
	}
	if want := "24/01/2014"; res.Item.Date != want {
		t.Errorf("res.Item.Date == %q; want %q", res.Item.Date, want)
	}

	srv.Respond("120NUN20140124    131049AOHUTL|AA2|AB1234|AJ|AH|AFInvalid Item|BLY|\r")
	res, err = DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgCheckout("HUTL", "2", "1234"), checkoutParse)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Item.TransactionFailed {
		t.Errorf("res.Item.TransactionFailed == false; want true")
	}
	if want := "Invalid Item"; res.Item.Status != want {
		t.Errorf("res.Item.Status == %q; want %q", res.Item.Status, want)
	}
}

func TestSIPItemStatus(t *testing.T) {
	srv := newSIPTestServer()
	defer srv.Close()

	initFn := initSIPConn(Config{SIPServer: srv.Addr(), RFIDTimeout: 1 * time.Second})
	srv.Respond("1801010120140228    110748AB1003010856677001|AO|AJ|\r")

	res, err := DoSIPCall(Config{RFIDTimeout: 1 * time.Second}, initFn, sipFormMsgItemStatus("1003010856677001"), itemStatusParse)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Item.TransactionFailed {
		t.Errorf("res.Item.TransactionFailed == false; want true")
	}
	if !res.Item.Unknown {
		t.Errorf("res.Item.Unknown == false; want true")
	}
}
