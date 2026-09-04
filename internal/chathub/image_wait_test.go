package chathub

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestImageGenerationWaitsPastChatIdleAndReturnsCompletedImage(t *testing.T) {
	serverErr := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			serverErr <- fmt.Errorf("read handshake: %w", err)
			return
		}
		if err = conn.WriteMessage(websocket.TextMessage, []byte(`{}`+rs)); err != nil {
			serverErr <- fmt.Errorf("write handshake: %w", err)
			return
		}
		for i := 0; i < 2; i++ {
			if _, _, err = conn.ReadMessage(); err != nil {
				serverErr <- fmt.Errorf("read request frame %d: %w", i, err)
				return
			}
		}

		pending := `{"type":1,"target":"update","arguments":[{"messages":[{"author":"bot","text":"Generating image","messageType":"Progress","contentType":"GraphicArt","contentOrigin":"ImageGeneration","addToChainOfThought":true,"contentGenerationProgressList":[{"contentType":"image","pollUrl":"fixture-poll","fileToken":"fixture-token","ImageReferenceUrls":[]}]}]}]}` + rs
		if err = conn.WriteMessage(websocket.TextMessage, []byte(pending)); err != nil {
			serverErr <- fmt.Errorf("write pending image frame: %w", err)
			return
		}

		time.Sleep(100 * time.Millisecond)
		completed := `{"type":1,"target":"update","arguments":[{"messages":[{"author":"bot","text":"Image ready","messageType":"Progress","contentType":"GraphicArt","contentOrigin":"ImageGeneration","addToChainOfThought":true,"contentGenerationProgressList":[{"contentType":"image","status":2,"pollUrl":"fixture-poll","fileToken":"fixture-token","ImageReferenceUrls":["https://designerapp.officeapps.live.com/designerapp/document.ashx?path=%2Ffixture%2FDallEGeneratedImages%2Ffixture.png&fileToken=fixture"]}]}]}]}` + rs
		if err = conn.WriteMessage(websocket.TextMessage, []byte(completed)); err != nil {
			serverErr <- fmt.Errorf("write completed image frame: %w", err)
			return
		}
		serverErr <- nil
	}))
	defer server.Close()

	localAddress := strings.TrimPrefix(server.URL, "https://")
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	dialer.NetDialTLSContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		tlsDialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{InsecureSkipVerify: true}} // Local TLS fixture.
		return tlsDialer.DialContext(ctx, network, localAddress)
	}
	client := NewClient()
	client.Dialer = &dialer
	client.Pool = nil
	client.ResponseIdleTimeout = 30 * time.Millisecond
	client.ImageResponseIdleTimeout = 250 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	result, err := client.Chat(ctx, Account{AccessToken: "token", OID: "oid", TID: "tid"}, Request{
		Text:            "generate an image",
		ImageGeneration: true,
	})
	if err != nil {
		t.Fatalf("image generation returned error: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 90*time.Millisecond {
		t.Fatalf("image request used ordinary idle timeout: %s", elapsed)
	}
	if len(result.Images) != 1 || !strings.Contains(result.Images[0], "designerapp.officeapps.live.com") {
		t.Fatalf("images=%v, want completed Designer image", result.Images)
	}
	if result.TerminalReason != "image_result" || result.Incomplete {
		t.Fatalf("unexpected terminal state: %#v", result)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
