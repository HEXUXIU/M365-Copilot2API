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
	conn      *websocket.Conn
	created   time.Time
	handshook bool
}

const maxPoolPerKey = 2

type ConnPool struct {
	mu     sync.Mutex
	conns  map[string][]*pooledConn // key = oid|tid, up to maxPoolPerKey connections
	dialer *websocket.Dialer
	header http.Header
	stop   chan struct{}
}

func NewConnPool(dialer *websocket.Dialer, header http.Header) *ConnPool {
	p := &ConnPool{
		conns:  make(map[string][]*pooledConn),
		dialer: dialer,
		header: header,
		stop:   make(chan struct{}),
	}
	go p.gcLoop()
	return p
}

func (p *ConnPool) key(oid, tid string) string { return oid + "|" + tid }

func (p *ConnPool) Take(ctx context.Context, oid, tid string, wsURL string) (*websocket.Conn, bool, error) {
	_ = wsURL
	p.mu.Lock()
	key := p.key(oid, tid)
	conns := p.conns[key]
	for i := len(conns) - 1; i >= 0; i-- {
		pc := conns[i]
		if time.Since(pc.created) < 2*time.Minute && pc.handshook {
			p.conns[key] = append(conns[:i], conns[i+1:]...)
			p.mu.Unlock()
			return pc.conn, true, nil
		}
		pc.conn.Close()
		conns = append(conns[:i], conns[i+1:]...)
		p.conns[key] = conns
	}
	p.mu.Unlock()

	conn, resp, err := p.dialer.DialContext(ctx, wsURL, p.header.Clone())
	if err != nil {
		if resp != nil {
			log.Printf("[connpool] dial failed oid=%s status=%d", oid, resp.StatusCode)
		}
		return nil, false, err
	}
	return conn, false, nil
}

func (p *ConnPool) Warm(ctx context.Context, acc Account, wsURL string) {
	if wsURL == "" {
		return
	}
	key := p.key(acc.OID, acc.TID)

	p.mu.Lock()
	if len(p.conns[key]) >= maxPoolPerKey {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	conn, resp, err := p.dialer.DialContext(ctx, wsURL, p.header.Clone())
	if err != nil {
		if resp != nil {
			log.Printf("[connpool] warm dial failed oid=%s status=%d err=%v", acc.OID, resp.StatusCode, err)
		} else {
			log.Printf("[connpool] warm dial failed oid=%s err=%v", acc.OID, err)
		}
		return
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"protocol":"json","version":1}`+"\x1e")); err != nil {
		log.Printf("[connpool] warm handshake send failed: %v", err)
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		log.Printf("[connpool] warm handshake recv failed: %v", err)
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	p.mu.Lock()
	if len(p.conns[key]) >= maxPoolPerKey {
		conn.Close()
		p.mu.Unlock()
		return
	}
	p.conns[key] = append(p.conns[key], &pooledConn{conn: conn, created: time.Now(), handshook: true})
	p.mu.Unlock()

	log.Printf("[connpool] warmed connection oid=%s tid=%s", acc.OID, acc.TID)
}

func (p *ConnPool) Return(oid, tid string, conn *websocket.Conn) {
	if conn == nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})
	key := p.key(oid, tid)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.conns[key]) >= maxPoolPerKey {
		conn.Close()
		return
	}
	p.conns[key] = append(p.conns[key], &pooledConn{conn: conn, created: time.Now(), handshook: true})
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
	for k, conns := range p.conns {
		kept := conns[:0]
		for _, pc := range conns {
			if now.Sub(pc.created) > 2*time.Minute {
				pc.conn.Close()
			} else {
				kept = append(kept, pc)
			}
		}
		if len(kept) == 0 {
			delete(p.conns, k)
		} else {
			p.conns[k] = kept
		}
	}
}

func (p *ConnPool) Close() {
	close(p.stop)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, conns := range p.conns {
		for _, pc := range conns {
			pc.conn.Close()
		}
		delete(p.conns, k)
	}
}

func (p *ConnPool) Stats() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	details := make([]map[string]any, 0)
	for k, conns := range p.conns {
		for _, pc := range conns {
			total++
			details = append(details, map[string]any{"key": k, "age_ms": time.Since(pc.created).Milliseconds(), "handshook": pc.handshook})
		}
	}
	return map[string]any{"mode": "connpool", "pooled_connections": total, "details": details}
}

func (p *ConnPool) gcLoop() {
	ticker := time.NewTicker(30 * time.Second)
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
