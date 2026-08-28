package chathub

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type pooledConn struct {
	conn      *websocket.Conn
	created   time.Time
	handshook bool
}

const (
	defaultMaxPoolPerKey = 4
	defaultPoolConnTTL   = 2 * time.Minute
	maxPoolPerKeyLimit   = 16
)

type ConnPool struct {
	mu        sync.Mutex
	conns     map[string][]*pooledConn // key = oid|tid, up to maxPoolPerKey connections
	dialer    *websocket.Dialer
	header    http.Header
	stop      chan struct{}
	maxPerKey int
	connTTL   time.Duration
	warming   map[string]int
}

func NewConnPool(dialer *websocket.Dialer, header http.Header) *ConnPool {
	maxPerKey := defaultMaxPoolPerKey
	if raw := os.Getenv("M365_WS_POOL_SIZE"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > maxPoolPerKeyLimit {
				parsed = maxPoolPerKeyLimit
			}
			maxPerKey = parsed
		}
	}
	connTTL := defaultPoolConnTTL
	if raw := os.Getenv("M365_WS_POOL_TTL_SECONDS"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 30 {
			connTTL = time.Duration(parsed) * time.Second
		}
	}
	p := &ConnPool{
		conns:     make(map[string][]*pooledConn),
		dialer:    dialer,
		header:    header,
		stop:      make(chan struct{}),
		maxPerKey: maxPerKey,
		connTTL:   connTTL,
		warming:   make(map[string]int),
	}
	go p.gcLoop()
	return p
}

func (p *ConnPool) key(oid, tid string) string { return oid + "|" + tid }

// Capacity reports the configured number of standby connections per account.
// It lets startup preheating match the runtime pool size without duplicating
// configuration parsing in the web package.
func (p *ConnPool) Capacity() int {
	if p == nil || p.maxPerKey <= 0 {
		return defaultMaxPoolPerKey
	}
	return p.maxPerKey
}

func (p *ConnPool) Take(ctx context.Context, oid, tid string, wsURL string) (*websocket.Conn, bool, error) {
	_ = wsURL
	p.mu.Lock()
	key := p.key(oid, tid)
	conns := p.conns[key]
	for i := len(conns) - 1; i >= 0; i-- {
		pc := conns[i]
		if time.Since(pc.created) < p.connTTL && pc.handshook {
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
	// Count in-flight dials as occupied slots. This prevents a startup burst
	// or concurrent completions from over-dialing the same account.
	if len(p.conns[key])+p.warming[key] >= p.maxPerKey {
		p.mu.Unlock()
		return
	}
	p.warming[key]++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.warming[key] <= 1 {
			delete(p.warming, key)
		} else {
			p.warming[key]--
		}
		p.mu.Unlock()
	}()

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
	if len(p.conns[key]) >= p.maxPerKey {
		conn.Close()
		p.mu.Unlock()
		return
	}
	p.conns[key] = append(p.conns[key], &pooledConn{conn: conn, created: time.Now(), handshook: true})
	p.mu.Unlock()

	log.Printf("[connpool] warmed connection oid=%s tid=%s", acc.OID, acc.TID)
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
	for k, conns := range p.conns {
		kept := conns[:0]
		for _, pc := range conns {
			if now.Sub(pc.created) > p.connTTL {
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
	return map[string]any{"mode": "connpool", "pooled_connections": total, "max_per_key": p.maxPerKey, "ttl_seconds": int(p.connTTL.Seconds()), "details": details}
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
