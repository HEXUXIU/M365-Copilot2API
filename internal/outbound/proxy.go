package outbound

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"
)

const EnvProxy = "M365_OUTBOUND_PROXY"

type Clients struct {
	HTTP      *http.Client
	WebSocket *websocket.Dialer
}

var (
	clientsMu sync.RWMutex
	clients   = directClients()
)

func directClients() *Clients {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Clients{
		HTTP: &http.Client{Transport: transport},
		WebSocket: &websocket.Dialer{
			HandshakeTimeout: 20 * time.Second,
			ReadBufferSize:   1024 * 1024,
			WriteBufferSize:  64 * 1024,
		},
	}
}

func ConfigureFromEnv() error {
	return Configure(os.Getenv(EnvProxy))
}

func Configure(raw string) error {
	configured, err := New(raw)
	if err != nil {
		return err
	}
	clientsMu.Lock()
	clients = configured
	clientsMu.Unlock()
	return nil
}

func HTTPClient() *http.Client {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	return clients.HTTP
}

func WebSocketDialer() *websocket.Dialer {
	clientsMu.RLock()
	defer clientsMu.RUnlock()
	copy := *clients.WebSocket
	return &copy
}

func ValidateProxyURL(raw string) error {
	_, err := New(raw)
	return err
}

func New(raw string) (*Clients, error) {
	configured := directClients()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return configured, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("outbound proxy must be a complete socks5://, http://, or https:// URL")
	}
	if u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("outbound proxy URL must not include a path, query, or fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		configured.HTTP.Transport.(*http.Transport).Proxy = http.ProxyURL(u)
		configured.WebSocket.Proxy = http.ProxyURL(u)
	case "https":
		configured.HTTP.Transport.(*http.Transport).Proxy = http.ProxyURL(u)
		configured.WebSocket.NetDialContext = httpsProxyDialer{proxyURL: u}.DialContext
	case "socks5":
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("configure SOCKS5 proxy: %w", err)
		}
		contextDialer := socksContextDialer{dialer: dialer}
		configured.HTTP.Transport.(*http.Transport).DialContext = contextDialer.DialContext
		configured.WebSocket.NetDialContext = contextDialer.DialContext
	default:
		return nil, fmt.Errorf("outbound proxy scheme %q is unsupported; use socks5, http, or https", u.Scheme)
	}
	return configured, nil
}

type httpsProxyDialer struct {
	proxyURL *url.URL
}

func (d httpsProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("HTTPS proxy only supports tcp, got %q", network)
	}
	proxyAddress := d.proxyURL.Host
	if d.proxyURL.Port() == "" {
		proxyAddress = net.JoinHostPort(d.proxyURL.Hostname(), "443")
	}
	rawConn, err := (&net.Dialer{}).DialContext(ctx, network, proxyAddress)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(rawConn, &tls.Config{ServerName: d.proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	connectReq := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if d.proxyURL.User != nil {
		password, _ := d.proxyURL.User.Password()
		connectReq.SetBasicAuth(d.proxyURL.User.Username(), password)
	}
	if err := connectReq.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, connectReq)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("HTTPS proxy CONNECT %s: %s", address, response.Status)
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type socksContextDialer struct {
	dialer proxy.Dialer
}

func (d socksContextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		conn, err := d.dialer.Dial(network, address)
		resultCh <- result{conn: conn, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.conn, result.err
	case <-ctx.Done():
		go func() {
			result := <-resultCh
			if result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
