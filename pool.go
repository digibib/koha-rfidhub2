package main

import (
	"net"
	"sync"
)

type connFactory func() (net.Conn, error)

// pool is a SIP-connection pool.
type pool struct {
	factory connFactory
	conns   chan net.Conn
	mu      sync.Mutex
	failing map[net.Conn]bool
}

func newPool(maxN int, fn connFactory) *pool {
	p := pool{
		conns:   make(chan net.Conn, maxN),
		failing: make(map[net.Conn]bool),
		factory: fn,
	}

	return &p
}

func (p *pool) get() (net.Conn, error) {
	select {
	case conn := <-p.conns:
		return conn, nil
	default:
		return p.factory()
	}
}

func (p *pool) put(conn net.Conn) {
	p.mu.Lock()

	if p.failing[conn] {
		delete(p.failing, conn)
		conn.Close()
		return
	}
	p.mu.Unlock()

	select {
	case p.conns <- conn:
	default:
		// pool is full
		conn.Close()
	}
}

func (p *pool) isFailing(conn net.Conn) {
	p.mu.Lock()
	p.failing[conn] = true
	p.mu.Unlock()
}
