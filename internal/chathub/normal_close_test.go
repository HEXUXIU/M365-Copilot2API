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

func TestChatCompletesAfterSuccessfulResultAndNormalClose(t *testing.T) {
	const answer = "complete answer"
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
		frames := []string{
			`{"type":1,"target":"update","arguments":[{"messages":[{"author":"bot","text":"complete answer","messageType":""}]}]}` + rs,
			`{"type":2,"item":{"result":{"value":"Success","message":"complete answer"}}}` + rs,
		}
		for i, frame := range frames {
			if err = conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				serverErr <- fmt.Errorf("write response frame %d: %w", i, err)
				return
			}
		}
		if err = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "normal"), time.Now().Add(time.Second)); err != nil {
			serverErr <- fmt.Errorf("write normal close: %w", err)
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

	var streamed strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := client.ChatWithDelta(ctx, Account{AccessToken: "token", OID: "oid", TID: "tid"}, Request{Text: "prompt"}, func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil {
		select {
		case serverSideErr := <-serverErr:
			t.Fatalf("chat returned error after successful result: %v (server: %v)", err, serverSideErr)
		default:
			t.Fatalf("chat returned error after successful result: %v", err)
		}
	}
	if result.Text != answer || streamed.String() != answer {
		t.Fatalf("result=%q streamed=%q, want %q", result.Text, streamed.String(), answer)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
