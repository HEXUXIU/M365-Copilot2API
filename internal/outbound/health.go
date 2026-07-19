package outbound

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

func redactProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u.String()
}

func (p *Pool) setHealth(raw, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if e.raw == raw {
			e.health = status
			return
		}
	}
}
func (p *Pool) Check(ctx context.Context, raw string) (time.Duration, error) {
	p.mu.Lock()
	var e *poolEntry
	for _, item := range p.entries {
		if item.raw == raw {
			e = item
			break
		}
	}
	p.mu.Unlock()
	if e == nil {
		return 0, http.ErrNoLocation
	}
	target := os.Getenv("M365_PROXY_HEALTH_URL")
	if target == "" {
		target = "https://www.msftconnecttest.com/connecttest.txt"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := e.clients.HTTP.Do(req)
	lat := time.Since(start)
	if err != nil {
		p.mark(raw, err)
		p.setHealth(raw, "unreachable")
		log.Printf("proxy health failed proxy=%s target=%s latency=%s err=%v", redactProxy(raw), target, lat, err)
		return lat, err
	}
	status := resp.Status
	code := resp.StatusCode
	resp.Body.Close()
	p.mark(raw, nil)
	if code >= 500 {
		p.setHealth(raw, "upstream_error")
		return lat, fmt.Errorf("proxy reachable; upstream returned %s", status)
	}
	p.setHealth(raw, "reachable")
	if code >= 400 {
		return lat, fmt.Errorf("proxy reachable; target returned %s", status)
	}
	return lat, nil
}
func (p *Pool) CheckAll(ctx context.Context) []map[string]any {
	p.mu.Lock()
	raws := make([]string, 0, len(p.entries))
	for _, e := range p.entries {
		raws = append(raws, e.raw)
	}
	p.mu.Unlock()
	for _, raw := range raws {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		lat, err := p.Check(c, raw)
		cancel()
		p.mu.Lock()
		for _, e := range p.entries {
			if e.raw == raw {
				e.lastCheck = time.Now()
				e.latency = lat
				e.lastError = ""
				if err != nil {
					e.lastError = err.Error()
				}
				break
			}
		}
		p.mu.Unlock()
	}
	return p.List()
}
