package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ToolProvider is the interface for discovering and invoking tools.
type ToolProvider interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (CallResult, error)
}

// GlobalToolRegistry holds the globally registered tools that are available
// to all MCP sessions, not just the session that created them.
var GlobalToolRegistry = &toolRegistry{tools: []Tool{}}

type toolRegistry struct {
	mu    sync.RWMutex
	tools []Tool
}

// RegisterTools adds tools to the global registry.
func (r *toolRegistry) RegisterTools(tools []Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append([]Tool(nil), tools...)
}

// ListTools returns the currently registered tools.
func (r *toolRegistry) ListTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Tool(nil), r.tools...)
}

// ClearTools clears all tools from the global registry.
func (r *toolRegistry) ClearTools() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = []Tool{}
}

// GlobalRegistry is a global registry of MCP sessions, keyed by session ID.
var GlobalRegistry = &sessionRegistry{sessions: map[string]*session{}}

// HandleToolsList returns the currently registered tools as JSON. Mount at /v1/mcp/tools.
func HandleToolsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	tools := GlobalToolRegistry.ListTools()
	if tools == nil {
		tools = []Tool{}
	}
	log.Printf("[mcp-tools] HandleToolsList called, returning %d tools", len(tools))
	json.NewEncoder(w).Encode(map[string]any{"tools": tools})
}

type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	id       string
	provider ToolProvider
	created  time.Time
	msgCh    chan json.RawMessage
	done     chan struct{}
}

// RegisterSession creates a new MCP session with the given tool provider and returns the session ID.
func (r *sessionRegistry) RegisterSession(provider ToolProvider) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	r.sessions[id] = &session{
		id:       id,
		provider: provider,
		created:  time.Now(),
		msgCh:    make(chan json.RawMessage, 64),
		done:     make(chan struct{}),
	}
	return id
}

// UnregisterSession removes a session from the registry.
func (r *sessionRegistry) UnregisterSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		close(s.done)
		delete(r.sessions, id)
	}
}

func (r *sessionRegistry) getSession(id string) *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

// HandleSSE handles MCP SSE connections. Mount at /v1/mcp/sse.
func HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a new session
	sessionID := GlobalRegistry.RegisterSession(nil)
	sess := GlobalRegistry.getSession(sessionID)
	defer GlobalRegistry.UnregisterSession(sessionID)

	absPath := "/v1/mcp/message"
	if r.TLS != nil {
		fmt.Fprintf(w, "event: endpoint\ndata: https://%s%s?sessionId=%s\n\n", r.Host, absPath, sessionID)
	} else {
		fmt.Fprintf(w, "event: endpoint\ndata: http://%s%s?sessionId=%s\n\n", r.Host, absPath, sessionID)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.done:
			return
		case msg, ok := <-sess.msgCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(msg))
			flusher.Flush()
		}
	}
}

// HandleMessage handles MCP JSON-RPC messages. Mount at /v1/mcp/message.
func HandleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		json.NewEncoder(w).Encode(newRPCError(nil, -32000, "sessionId required"))
		return
	}

	sess := GlobalRegistry.getSession(sessionID)
	if sess == nil {
		json.NewEncoder(w).Encode(newRPCError(nil, -32000, "session not found"))
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(newRPCError(nil, -32700, "parse error: "+err.Error()))
		return
	}

	resp := handleRPC(r.Context(), sess, &req)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	b, _ := json.Marshal(resp)
	select {
	case sess.msgCh <- b:
	default:
		log.Printf("[mcp] dropped response for session %s (channel full)", sessionID)
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// SetSessionTools sets the tools for an existing session. Called after SSE is established.
func SetSessionTools(sessionID string, provider ToolProvider) {
	sess := GlobalRegistry.getSession(sessionID)
	if sess != nil {
		sess.provider = provider
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}
type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newRPCError(id *int64, code int, msg string) *jsonRPCResponse {
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}
func jsonRPCResult(id *int64, result any) *jsonRPCResponse {
	b, _ := json.Marshal(result)
	return &jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: b}
}

func handleRPC(ctx context.Context, sess *session, req *jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return jsonRPCResult(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "m365-native", "version": "0.1.0"},
		})
	case "tools/list":
	   // First check session-specific tools, then fall back to global registry
	   var tools []Tool
	   if sess.provider != nil {
	    t, err := sess.provider.ListTools(ctx)
	    if err == nil && len(t) > 0 {
	     tools = t
	    }
	   }
	   if len(tools) == 0 {
	    tools = GlobalToolRegistry.ListTools()
	   }
	   if tools == nil {
	    tools = []Tool{}
	   }
	   return jsonRPCResult(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		if sess.provider == nil {
			return newRPCError(req.ID, -32603, "no tools available")
		}
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return newRPCError(req.ID, -32602, "invalid params: "+err.Error())
		}
		result, err := sess.provider.CallTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return jsonRPCResult(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("error: %v", err)}},
				"isError": true,
			})
		}
		return jsonRPCResult(req.ID, result)
	case "notifications/initialized":
		return nil
	default:
		return newRPCError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// StaticToolProvider holds a static list of tools.
type StaticToolProvider struct {
	mu     sync.RWMutex
	tools  []Tool
	onCall func(ctx context.Context, name string, args map[string]any) (CallResult, error)
}

func NewStaticToolProvider(tools []Tool, onCall func(ctx context.Context, name string, args map[string]any) (CallResult, error)) *StaticToolProvider {
	return &StaticToolProvider{tools: tools, onCall: onCall}
}
func (p *StaticToolProvider) ListTools(ctx context.Context) ([]Tool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Tool(nil), p.tools...), nil
}
func (p *StaticToolProvider) CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	if p.onCall == nil {
		return CallResult{}, fmt.Errorf("tool %s not implemented", name)
	}
	return p.onCall(ctx, name, args)
}

// ConvertTools converts OpenAI-format tools to MCP tools.
func ConvertTools(tools []Tool) []Tool {
	return tools
}