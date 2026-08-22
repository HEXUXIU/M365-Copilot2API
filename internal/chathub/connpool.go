package chathub

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type pooledConn struct {
	conn    *websocket.Conn
	created time.Time
}

type ConnPool struct {
	mu      sync.Mutex
	conns   map[string]*pooledConn // key = oid|tid
	dialer  *websocket.Dialer
	header  http.Header
	baseURL string // pre-built URL without session/conversation IDs
	stop    chan struct{}
}

func NewConnPool(dialer *websocket.Dialer, header http.Header) *ConnPool {
	p := &ConnPool{
		conns:  make(map[string]*pooledConn),
		dialer: dialer,
		header: header,
		stop:   make(chan struct{}),
	}
	go p.gcLoop()
	return p
}

func (p *ConnPool) key(oid, tid string) string { return oid + "|" + tid }

func (p *ConnPool) Take(ctx context.Context, oid, tid string, wsURL string) (*websocket.Conn, bool, error) {
	p.mu.Lock()
	pc := p.conns[p.key(oid, tid)]
	if pc != nil {
		delete(p.conns, p.key(oid, tid))
		p.mu.Unlock()
		pc.conn.Close()
	} else {
		p.mu.Unlock()
	}
	conn, resp, err := p.dialer.DialContext(ctx, wsURL, p.header.Clone())
	if err != nil {
		if resp != nil {
			log.Printf("[connpool] dial failed oid=%s status=%d", oid, resp.StatusCode)
		}
		return nil, false, err
	}
	return conn, false, nil
}

func (p *ConnPool) Return(oid, tid string, conn *websocket.Conn) {
	if conn != nil {
		conn.Close()
	}
}

func (p *ConnPool) Discard(oid, tid string, conn *websocket.Conn) {
	if conn != nil {
		conn.Close()
	}
}

func (p *ConnPool) GC() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, pc := range p.conns {
		if now.Sub(pc.created) > 5*time.Minute {
			pc.conn.Close()
			delete(p.conns, k)
		}
	}
}

func (p *ConnPool) Close() {
	close(p.stop)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, pc := range p.conns {
		pc.conn.Close()
		delete(p.conns, k)
	}
}

func (p *ConnPool) Stats() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return map[string]any{"pooled_connections": len(p.conns)}
}

func (p *ConnPool) gcLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.GC()
		}
	}
}
