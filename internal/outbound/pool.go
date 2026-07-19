package outbound

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type poolEntry struct {
	raw       string
	clients   *Clients
	failures  int
	cooldown  time.Time
	lastCheck time.Time
	latency   time.Duration
	lastError string
	health    string
}
type Pool struct {
	mu      sync.Mutex
	entries []*poolEntry
	next    int
}

func NewPool(raw []string) (*Pool, error) {
	p := &Pool{}
	seen := map[string]bool{}
	for _, v := range raw {
		if v == "" || seen[v] {
			continue
		}
		c, err := New(v)
		if err != nil {
			return nil, fmt.Errorf("proxy %q: %w", v, err)
		}
		seen[v] = true
		p.entries = append(p.entries, &poolEntry{raw: v, clients: c})
	}
	return p, nil
}
func (p *Pool) pick() *poolEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return nil
	}
	now := time.Now()
	for i := 0; i < len(p.entries); i++ {
		e := p.entries[(p.next+i)%len(p.entries)]
		if now.Before(e.cooldown) {
			continue
		}
		p.next = (p.next + i + 1) % len(p.entries)
		return e
	}
	e := p.entries[p.next%len(p.entries)]
	p.next = (p.next + 1) % len(p.entries)
	return e
}
func (p *Pool) mark(raw string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.raw == raw {
			if err == nil {
				e.failures = 0
				e.cooldown = time.Time{}
			} else {
				e.failures++
				d := time.Duration(e.failures) * 2 * time.Second
				if d > 2*time.Minute {
					d = 2 * time.Minute
				}
				e.cooldown = time.Now().Add(d)
			}
			return
		}
	}
}
func (p *Pool) HTTPClient() *http.Client {
	if e := p.pick(); e != nil {
		return &http.Client{Transport: &poolRoundTripper{pool: p, entry: e, base: e.clients.HTTP.Transport}}
	}
	return directClients().HTTP
}
func (p *Pool) WebSocketDialer() *websocket.Dialer {
	base := directClients().WebSocket
	baseDialer := &net.Dialer{}
	var mu sync.Mutex
	var sticky *poolEntry
	base.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		e := sticky
		if e == nil {
			e = p.pick()
			sticky = e
		}
		mu.Unlock()
		if e == nil {
			return baseDialer.DialContext(ctx, network, address)
		}
		dial := e.clients.WebSocket.NetDialContext
		if dial == nil {
			dial = baseDialer.DialContext
		}
		conn, err := dial(ctx, network, address)
		p.mark(e.raw, err)
		if err != nil {
			mu.Lock()
			if sticky == e {
				sticky = nil
			}
			mu.Unlock()
		}
		return conn, err
	}
	return base
}
func (p *Pool) List() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, map[string]any{"url": e.raw, "failures": e.failures, "cooldownUntil": e.cooldown, "lastCheck": e.lastCheck, "latencyMs": e.latency.Milliseconds(), "lastError": e.lastError, "health": e.health})
	}
	return out
}
func (p *Pool) Remove(raw string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, e := range p.entries {
		if e.raw == raw {
			p.entries = append(p.entries[:i], p.entries[i+1:]...)
			return
		}
	}
}

type poolRoundTripper struct {
	pool  *Pool
	entry *poolEntry
	base  http.RoundTripper
}

func (t *poolRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	t.pool.mark(t.entry.raw, err)
	return resp, err
}
