// LongContextProbe sends a very large prompt to each supported protocol and
// checks that the gateway forwards it intact: the model must be able to echo a
// token planted at the start and at the end of the context, which proves
// nothing was silently truncated in the middle.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type payload struct {
	Model     string           `json:"model"`
	Stream    bool             `json:"stream,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
	Messages  []map[string]any `json:"messages"`
	Tools     []any            `json:"tools,omitempty"`
}

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:4141", "gateway base URL")
	key := flag.String("key", "", "api key")
	tokens := flag.Int("tokens", 100000, "approximate prompt tokens")
	flag.Parse()

	if *key == "" {
		fmt.Fprintln(os.Stderr, "-key is required")
		os.Exit(2)
	}

	const head = "NEEDLE_HEAD_7f3a9c"
	const tail = "NEEDLE_TAIL_4b2e81"

	body := buildContext(*tokens, head, tail)
	words := len(strings.Fields(body))
	fmt.Printf("context: ~%d words (target ~%d tokens)\n", words, *tokens)

	client := &http.Client{
		Timeout:   600 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 8},
	}

	type result struct {
		name    string
		status  int
		elapsed time.Duration
		text    string
		err     string
	}
	var results []result

	for _, ep := range []struct {
		name string
		kind string
	}{
		{"chat/completions", "chat"},
		{"responses", "responses"},
		{"messages", "messages"},
	} {
		r := run(client, *gateway, *key, ep.name, ep.kind, body, head, tail)
		results = append(results, r)
		fmt.Printf("  %-18s status=%d %.1fs head=%v tail=%v %s\n",
			r.name, r.status, r.elapsed.Seconds(),
			strings.Contains(r.text, head), strings.Contains(r.text, tail), r.err)
	}

	bad := 0
	for _, r := range results {
		if r.status != 200 || !strings.Contains(r.text, head) || !strings.Contains(r.text, tail) {
			bad++
			fmt.Printf("FAIL %s status=%d head=%v tail=%v %s\n",
				r.name, r.status,
				strings.Contains(r.text, head), strings.Contains(r.text, tail),
				trunc(r.text))
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}

func trunc(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func buildContext(targetWords int, head, tail string) string {
	var b strings.Builder
	b.WriteString("Below is filler context for a long-context test.\n")
	b.WriteString("FIRST SENTENCE: " + head + "\n")
	// Deterministic filler so the size is reproducible.
	for i := 0; b.Len() < targetWords*6; i++ {
		fmt.Fprintf(&b, "Filler line %d: lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor.\n", i)
	}
	b.WriteString("LAST SENTENCE: " + tail + "\n")
	b.WriteString("Reply with exactly two lines:\nFIRST SENTENCE value\nLAST SENTENCE value\n")
	return b.String()
}

func run(client *http.Client, gateway, key, name, kind, body, head, tail string) struct {
	name    string
	status  int
	elapsed time.Duration
	text    string
	err     string
} {
	var p payload
	switch kind {
	case "chat":
		p = payload{Model: "gpt-5.6-sol", MaxTokens: 200,
			Messages: []map[string]any{{"role": "user", "content": body}}}
	case "responses":
		p = payload{Model: "gpt-5.6-sol", MaxTokens: 200,
			Messages: []map[string]any{{"role": "user", "content": body}}}
	case "messages":
		p = payload{Model: "gpt-5.6-sol", MaxTokens: 200,
			Messages: []map[string]any{{"role": "user", "content": body}}}
	}

	var raw []byte
	var urlPath string
	switch kind {
	case "chat":
		raw, _ = json.Marshal(p)
		urlPath = "/v1/chat/completions"
	case "responses":
		raw, _ = json.Marshal(map[string]any{
			"model": p.Model, "input": []any{
				map[string]any{"type": "message", "role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": body}}},
			},
		})
		urlPath = "/v1/responses"
	case "messages":
		raw, _ = json.Marshal(map[string]any{
			"model": p.Model, "max_tokens": 200,
			"messages": []any{map[string]any{"role": "user", "content": body}},
		})
		urlPath = "/v1/messages"
	}

	req, _ := http.NewRequest("POST", strings.TrimRight(gateway, "/")+urlPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if kind == "messages" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return struct {
			name    string
			status  int
			elapsed time.Duration
			text    string
			err     string
		}{name, -1, time.Since(start), "", err.Error()}
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	elapsed := time.Since(start)

	text := extract(kind, buf)
	return struct {
		name    string
		status  int
		elapsed time.Duration
		text    string
		err     string
	}{name, resp.StatusCode, elapsed, text, ""}
}

func extract(kind string, body []byte) string {
	s := string(body)
	if kind == "chat" {
		var v struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(body, &v) == nil && len(v.Choices) > 0 {
			return v.Choices[0].Message.Content
		}
		return s
	}
	if kind == "responses" {
		var v struct {
			Output []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if json.Unmarshal(body, &v) == nil {
			var sb strings.Builder
			for _, o := range v.Output {
				for _, c := range o.Content {
					sb.WriteString(c.Text)
				}
			}
			return sb.String()
		}
	}
	var v struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(body, &v) == nil {
		var sb strings.Builder
		for _, c := range v.Content {
			sb.WriteString(c.Text)
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	return s
}