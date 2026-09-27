package mcp

import (
	"context"
	"testing"
	"time"
)

func TestToolCallQueueCapacity(t *testing.T) {
	q := NewToolCallQueue()
	first := q.Enqueue("first", nil)
	for i := 1; i < maxPendingToolCalls; i++ {
		q.Enqueue("tool", nil)
	}
	rejected := q.Enqueue("rejected", nil)
	if q.PendingCount() != maxPendingToolCalls {
		t.Fatalf("pending count = %d, want %d", q.PendingCount(), maxPendingToolCalls)
	}
	select {
	case <-rejected.ErrCh:
	default:
		t.Fatal("new call was not rejected when queue reached capacity")
	}
	if got := q.DequeueNonBlocking(); got != first {
		t.Fatalf("oldest queued call = %p, want %p", got, first)
	}
}

func TestToolCallQueueDequeueClearsSlot(t *testing.T) {
	q := NewToolCallQueue()
	call := q.Enqueue("tool", nil)
	if got := q.DequeueNonBlocking(); got != call {
		t.Fatalf("dequeued call = %p, want %p", got, call)
	}
	if q.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", q.PendingCount())
	}
}

func TestToolCallQueueCancellationLeavesNoPendingCall(t *testing.T) {
	q := NewToolCallQueue()
	p := NewMCPToolProvider(nil, q)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.CallTool(ctx, "tool", nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if q.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", q.PendingCount())
	}
}

func TestToolCallQueueDequeueDeadlineDoesNotLeak(t *testing.T) {
	q := NewToolCallQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if call := q.Dequeue(ctx); call != nil {
		t.Fatalf("dequeued unexpected call: %v", call)
	}
}

func TestToolCallQueueResolveAfterDequeueDeliversResult(t *testing.T) {
	q := NewToolCallQueue()
	call := q.Enqueue("tool", nil)
	if got := q.DequeueNonBlocking(); got != call {
		t.Fatalf("dequeued call = %p, want %p", got, call)
	}
	want := CallResult{Content: []map[string]any{{"type": "text", "text": "ok"}}}
	q.Resolve(call, want, nil)
	select {
	case got := <-call.ResultCh:
		if len(got.Content) != 1 || got.Content[0]["text"] != "ok" {
			t.Fatalf("unexpected result: %#v", got)
		}
	default:
		t.Fatal("dequeued call did not receive its result")
	}
}
