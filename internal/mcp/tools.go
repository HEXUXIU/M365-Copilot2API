package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type Resource struct {
	URI         string         `json:"uri"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	MIMEType    string         `json:"mimeType,omitempty"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     []byte `json:"blob,omitempty"`
}

type ResourceProvider interface {
	ListResources(ctx context.Context) ([]Resource, error)
	ReadResource(ctx context.Context, uri string) (ResourceContent, error)
}

type CallResult struct {
	Content        []map[string]any `json:"content,omitempty"`
	StructuredData any              `json:"structuredContent,omitempty"`
	IsError        bool             `json:"isError,omitempty"`
}

type ToolCache struct {
	mu    sync.RWMutex
	tools []Tool
}

func (c *ToolCache) Replace(tools []Tool) {
	copyTools := append([]Tool(nil), tools...)
	c.mu.Lock()
	c.tools = copyTools
	c.mu.Unlock()
}

func (c *ToolCache) Merge(tools []Tool) {
	c.mu.Lock()
	existing := map[string]bool{}
	for _, t := range c.tools {
		existing[t.Name] = true
	}
	for _, t := range tools {
		if !existing[t.Name] {
			c.tools = append(c.tools, t)
		}
	}
	c.mu.Unlock()
}

func (c *ToolCache) List() []Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Tool(nil), c.tools...)
}

func (c *ToolCache) Find(name string) (Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, tool := range c.tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

func (r CallResult) Text() string {
	var out []string
	for _, block := range r.Content {
		if typ, _ := block["type"].(string); typ != "text" {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n")
}

func (r CallResult) ContentJSON() []byte {
	b, _ := json.Marshal(r.Content)
	return b
}
