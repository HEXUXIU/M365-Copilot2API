package chathub

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
)

type ConnPool struct {
	dialer *websocket.Dialer
	header http.Header
}

func NewConnPool(dialer *websocket.Dialer, header http.Header) *ConnPool {
	return &ConnPool{dialer: dialer, header: header}
}

func (p *ConnPool) Take(ctx context.Context, oid, tid string, wsURL string) (*websocket.Conn, bool, error) {
	conn, resp, err := p.dialer.DialContext(ctx, wsURL, p.header.Clone())
	if err != nil {
		return nil, false, err
	}
	_ = resp
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
