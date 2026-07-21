package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PendingToolCall represents a tool call that is waiting to be executed by the client.
type PendingToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	ResultCh  chan CallResult
	ErrCh     chan error
	CreatedAt time.Time
}

// ToolCallQueue manages pending MCP tool calls and their results.
// It allows the MCP server's onCall handler to block until the client
// executes the tool and returns the result.
type ToolCallQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	pending  []*PendingToolCall
	nextID   int64
}

// NewToolCallQueue creates a new tool call queue.
func NewToolCallQueue() *ToolCallQueue {
	q := &ToolCallQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds a tool call to the queue and returns a channel that will receive the result.
// The caller should block on either ResultCh or ErrCh.
func (q *ToolCallQueue) Enqueue(name string, arguments map[string]any) *PendingToolCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	call := &PendingToolCall{
		ID:        fmt.Sprintf("mcp-tool-%d", q.nextID),
		Name:      name,
		Arguments: arguments,
		ResultCh:  make(chan CallResult, 1),
		ErrCh:     make(chan error, 1),
		CreatedAt: time.Now(),
	}
	q.pending = append(q.pending, call)
	q.cond.Broadcast()
	return call
}

// Dequeue waits for and returns the next pending tool call.
// Returns nil if the context is cancelled.
func (q *ToolCallQueue) Dequeue(ctx context.Context) *PendingToolCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pending) == 0 {
		ch := make(chan struct{})
		go func() {
			q.cond.Wait()
			close(ch)
		}()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}
	call := q.pending[0]
	q.pending = q.pending[1:]
	return call
}

// DequeueNonBlocking returns the next pending tool call without waiting.
// Returns nil if no pending calls.
func (q *ToolCallQueue) DequeueNonBlocking() *PendingToolCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	call := q.pending[0]
	q.pending = q.pending[1:]
	return call
}

// Resolve sends the result for a pending tool call, unblocking the onCall handler.
func (q *ToolCallQueue) Resolve(call *PendingToolCall, result CallResult, err error) {
	if err != nil {
		select {
		case call.ErrCh <- err:
		default:
		}
	} else {
		select {
		case call.ResultCh <- result:
		default:
		}
	}
}

// PendingCount returns the number of pending tool calls.
func (q *ToolCallQueue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// NewMCPToolProvider creates a ToolProvider that uses the ToolCallQueue for async tool execution.
func NewMCPToolProvider(tools []Tool, queue *ToolCallQueue) *MCPToolProvider {
	return &MCPToolProvider{
		tools: tools,
		queue: queue,
	}
}

// MCPToolProvider is a ToolProvider that enqueues tool calls for async execution.
type MCPToolProvider struct {
	mu    sync.RWMutex
	tools []Tool
	queue *ToolCallQueue
}

func (p *MCPToolProvider) ListTools(ctx context.Context) ([]Tool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Tool(nil), p.tools...), nil
}

func (p *MCPToolProvider) CallTool(ctx context.Context, name string, arguments map[string]any) (CallResult, error) {
	// Enqueue the tool call for the main flow to pick up
	call := p.queue.Enqueue(name, arguments)

	// Try to wait for the result, but return immediately if the client
	// hasn't responded within a short timeout. The actual tool execution
	// is handled by the standard OpenAI tool calling flow.
	timeout := 30 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-call.ResultCh:
		return result, nil
	case err := <-call.ErrCh:
		return CallResult{}, err
	case <-timer.C:
		// Timeout - the tool call has been returned to the user's client.
		// Return a pending result so the MCP client knows the tool is being
		// executed. The actual result will be forwarded in a subsequent turn.
		return CallResult{
			Content: []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Tool call %s has been forwarded to the client for execution. The result will be provided in a subsequent turn.", name)},
			},
		}, nil
	case <-ctx.Done():
		return CallResult{}, ctx.Err()
	}
}

// UpdateTools replaces the tool list for the provider.
func (p *MCPToolProvider) UpdateTools(tools []Tool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tools = append([]Tool(nil), tools...)
}