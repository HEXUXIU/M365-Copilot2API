package outbound

import (
	"net/http"
	"testing"
)

func TestNewSupportsHTTPHTTPSAndSOCKS5(t *testing.T) {
	for _, raw := range []string{
		"http://proxy.example:8080",
		"https://proxy.example:8443",
		"socks5://proxy.example:1080",
	} {
		t.Run(raw, func(t *testing.T) {
			clients, err := New(raw)
			if err != nil {
				t.Fatal(err)
			}
			transport := clients.HTTP.Transport.(*http.Transport)
			if clients.WebSocket == nil {
				t.Fatal("missing websocket dialer")
			}
			if len(raw) >= 6 && raw[:6] == "socks5" {
				if transport.DialContext == nil || clients.WebSocket.NetDialContext == nil {
					t.Fatal("SOCKS5 dialer was not installed for HTTP and WebSocket")
				}
				return
			}
			request, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
			proxyURL, err := transport.Proxy(request)
			if err != nil {
				t.Fatal(err)
			}
			if proxyURL == nil || proxyURL.String() != raw {
				t.Fatalf("HTTP proxy = %v, want %s", proxyURL, raw)
			}
			if len(raw) >= 8 && raw[:8] == "https://" {
				if clients.WebSocket.Proxy != nil || clients.WebSocket.NetDialContext == nil {
					t.Fatal("HTTPS proxy was not installed for WebSocket")
				}
				return
			}
			webSocketProxy, err := clients.WebSocket.Proxy(request)
			if err != nil {
				t.Fatal(err)
			}
			if webSocketProxy == nil || webSocketProxy.String() != raw {
				t.Fatalf("WebSocket proxy = %v, want %s", webSocketProxy, raw)
			}
		})
	}
}

func TestNewRejectsUnsupportedOrIncompleteProxy(t *testing.T) {
	for _, raw := range []string{"ftp://proxy.example:21", "socks5://", "proxy.example:8080", "http://proxy.example:8080/path"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := New(raw); err == nil {
				t.Fatal("accepted invalid proxy URL")
			}
		})
	}
}

func TestConfigureFromEnv(t *testing.T) {
	t.Setenv(EnvProxy, "socks5://proxy.example:1080")
	if err := ConfigureFromEnv(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Configure("") })
	if HTTPClient().Transport.(*http.Transport).DialContext == nil {
		t.Fatal("configured HTTP client does not use SOCKS5")
	}
	if WebSocketDialer().NetDialContext == nil {
		t.Fatal("configured WebSocket dialer does not use SOCKS5")
	}
}
