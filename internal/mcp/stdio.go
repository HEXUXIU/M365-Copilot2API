package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
)

// StdioClient 通过子进程 stdin/stdout 交换 JSON-RPC 消息，用于测试与轻量桥接。
type StdioClient struct {
	cmd     *exec.Cmd
	stdin   interface{ Write([]byte) (int, error) }
	stdout  *bufio.Scanner
	mu      sync.Mutex
	pending map[int64]chan json.RawMessage
	nextID  int64
	done    chan struct{}
}

// StartStdio 启动一个子进程作为 MCP JSON-RPC 服务器并返回客户端。
// opts 保留用于未来的启动选项，当前忽略。
func StartStdio(ctx context.Context, command string, args []string, opts any) (*StdioClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &StdioClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
		pending: map[int64]chan json.RawMessage{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *StdioClient) readLoop() {
	defer close(c.done)
	for c.stdout.Scan() {
		line := c.stdout.Bytes()
		if len(line) == 0 {
			continue
		}
		var meta struct {
			ID *int64 `json:"id"`
		}
		if err := json.Unmarshal(line, &meta); err != nil || meta.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*meta.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- append(json.RawMessage(nil), line...):
			default:
			}
		}
	}
}

func (c *StdioClient) call(ctx context.Context, method string, id int64, params any) (json.RawMessage, error) {
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	c.mu.Lock()
	ch := make(chan json.RawMessage, 1)
	c.pending[id] = ch
	if _, err := c.stdin.Write(b); err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	select {
	case raw := <-ch:
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Initialize 发送初始化握手。
func (c *StdioClient) Initialize(ctx context.Context) error {
	_, err := c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "m365-copilot2api-mcp-stdio", "version": "0.1.0"},
	})
	return err
}

func (c *StdioClient) sendRequest(ctx context.Context, method string, params any) (map[string]any, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	raw, err := c.call(ctx, method, id, params)
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if e, ok := obj["error"]; ok && e != nil {
		return nil, fmt.Errorf("rpc error: %v", e)
	}
	return obj, nil
}

// ListTools 列出子进程提供的工具。
func (c *StdioClient) ListTools(ctx context.Context) ([]Tool, error) {
	obj, err := c.sendRequest(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(obj["result"])
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool 调用子进程提供的工具。
func (c *StdioClient) CallTool(ctx context.Context, name string, arguments map[string]any) (CallResult, error) {
	var result CallResult
	obj, err := c.sendRequest(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return result, err
	}
	res, _ := json.Marshal(obj["result"])
	if err := json.Unmarshal(res, &result); err != nil {
		return result, err
	}
	return result, nil
}

// Close 终止子进程。
func (c *StdioClient) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}