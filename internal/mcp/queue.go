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
	mu      sync.Mutex
	pending []*PendingToolCall
	nextID  int64
	notify  chan struct{}
}

const maxPendingToolCalls = 1024

// NewToolCallQueue creates a new tool call queue.
func NewToolCallQueue() *ToolCallQueue {
	return &ToolCallQueue{notify: make(chan struct{}, 1)}
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
	if len(q.pending) >= maxPendingToolCalls {
		select {
		case call.ErrCh <- fmt.Errorf("MCP tool call queue capacity exceeded"):
		default:
		}
		return call
	}
	q.pending = append(q.pending, call)
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return call
}

func (q *ToolCallQueue) remove(call *PendingToolCall) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, pending := range q.pending {
		if pending != call {
			continue
		}
		copy(q.pending[i:], q.pending[i+1:])
		q.pending[len(q.pending)-1] = nil
		q.pending = q.pending[:len(q.pending)-1]
		return true
	}
	return false
}

// Dequeue waits for and returns the next pending tool call.
// Returns nil if the context is cancelled or no call arrives before the context deadline.
func (q *ToolCallQueue) Dequeue(ctx context.Context) *PendingToolCall {
	for {
		q.mu.Lock()
		if len(q.pending) > 0 {
			call := q.pending[0]
			q.pending[0] = nil
			q.pending = q.pending[1:]
			q.mu.Unlock()
			return call
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-q.notify:
		}
	}
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
	q.pending[0] = nil
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
		p.queue.remove(call)
		return CallResult{}, fmt.Errorf("MCP tool call %s timed out waiting for the client result", name)
	case <-ctx.Done():
		p.queue.remove(call)
		return CallResult{}, ctx.Err()
	}
}

// UpdateTools replaces the tool list for the provider.
func (p *MCPToolProvider) UpdateTools(tools []Tool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tools = append([]Tool(nil), tools...)
}
