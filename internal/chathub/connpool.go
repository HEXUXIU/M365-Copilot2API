package chathub

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type ConnPool struct {
	mu     sync.Mutex
	dialer *websocket.Dialer
	header http.Header
}

func NewConnPool(dialer *websocket.Dialer, header http.Header) *ConnPool {
	return &ConnPool{dialer: dialer, header: header}
}

func (p *ConnPool) Take(ctx context.Context, oid, tid string, wsURL string) (*websocket.Conn, bool, error) {
	p.mu.Lock()
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

func (p *ConnPool) Stats() map[string]any {
	return map[string]any{"pooled_connections": 0}
}
