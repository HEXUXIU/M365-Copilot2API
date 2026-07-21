package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is an MCP client that connects to an MCP server via HTTP SSE.
// It implements the MCP client protocol: discover tools, invoke tools.
type Client struct {
	serverURL string
	httpClient *http.Client
	sessionID  string
	mu         sync.Mutex
	connected  bool
	msgCh      chan json.RawMessage
	done       chan struct{}
}

// NewClient creates a new MCP client that connects to the given server URL.
func NewClient(serverURL string) *Client {
	return &Client{
		serverURL:  serverURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		msgCh:      make(chan json.RawMessage, 64),
		done:       make(chan struct{}),
	}
}

// Connect establishes the SSE connection to the MCP server and initializes the session.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}

	// Establish SSE connection
	sseURL := c.serverURL
	if !strings.Contains(sseURL, "/sse") {
		sseURL = strings.TrimRight(sseURL, "/") + "/sse"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return fmt.Errorf("sse request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sse connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("sse status: %s", resp.Status)
	}

	// Parse the SSE response to get the session ID and endpoint
	scanner := bufio.NewScanner(resp.Body)
	messageURL := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			fmt.Printf("[mcp-client] sse data: %s\n", data)
		}
		if strings.HasPrefix(line, "event: endpoint") {
			// Next line should contain the data with the message URL
			continue
		}
		if strings.HasPrefix(line, "data: /message?") {
			messageURL = strings.TrimPrefix(line, "data: ")
			// Parse session ID from the URL
			if idx := strings.Index(messageURL, "sessionId="); idx >= 0 {
				c.sessionID = messageURL[idx+10:]
			}
			break
		}
	}

	if c.sessionID == "" {
		resp.Body.Close()
		return fmt.Errorf("no session ID received from MCP server")
	}

	// Start background goroutine to read SSE events
	c.connected = true
	go c.readSSE(resp.Body)

	// Initialize the session
	err = c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "m365-native-mcp-client", "version": "0.1.0"},
	})
	if err != nil {
		c.Close()
		return fmt.Errorf("initialize: %w", err)
	}

	// Send initialized notification
	_ = c.sendNotification("notifications/initialized", nil)

	return nil
}

func (c *Client) readSSE(body io.ReadCloser) {
	defer body.Close()
	defer func() { c.connected = false }()
	
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var msg json.RawMessage
			if err := json.Unmarshal([]byte(data), &msg); err == nil {
				select {
				case c.msgCh <- msg:
				default:
				}
			}
		}
	}
}

// Close closes the MCP client connection.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var result struct {
		Tools []Tool `json:"tools"`
	}
	err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	// Wait for the response
	select {
	case msg := <-c.msgCh:
		var resp struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("timeout waiting for tools/list response")
	}
	return result.Tools, nil
}

// CallTool invokes a tool on the MCP server.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (CallResult, error) {
	var result CallResult
	err := c.sendRequest(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return result, err
	}
	// Wait for the response
	select {
	case msg := <-c.msgCh:
		var resp struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			return result, err
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return result, err
		}
	case <-ctx.Done():
		return result, ctx.Err()
	case <-time.After(30 * time.Second):
		return result, fmt.Errorf("timeout waiting for tools/call response")
	}
	return result, nil
}

func (c *Client) sendRequest(ctx context.Context, method string, params any) error {
	id := time.Now().UnixNano()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)

	messageURL := fmt.Sprintf("%s/message?sessionId=%s", strings.TrimRight(strings.Split(c.serverURL, "/sse")[0], "/"), c.sessionID)
	
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messageURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) sendNotification(method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)

	messageURL := fmt.Sprintf("%s/message?sessionId=%s", strings.TrimRight(strings.Split(c.serverURL, "/sse")[0], "/"), c.sessionID)
	
	httpReq, err := http.NewRequest(http.MethodPost, messageURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Close closes the MCP client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return nil
	}
	c.connected = false
	close(c.done)
	return nil
}