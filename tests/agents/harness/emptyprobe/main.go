// EmptyReplyProbe fires requests until one comes back 200 with no assistant
// content, then dumps the raw SSE so the failure mode is visible.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:4141", "gateway base URL")
	key := flag.String("key", "", "api key")
	concurrency := flag.Int("c", 10, "concurrent requests")
	attempts := flag.Int("n", 60, "max requests")
	flag.Parse()

	payload := map[string]any{
		"model":      "gpt-5.6-sol",
		"stream":     true,
		"max_tokens": 700,
		"messages":   []any{map[string]any{"role": "user", "content": "Explain quicksort with a python code block and explain it line by line."}},
	}
	b, _ := json.Marshal(payload)

	client := &http.Client{
		Timeout:   180 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64},
	}

	var sent atomic.Int64
	var empties atomic.Int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, *concurrency)
	var mu sync.Mutex
	dump := ""

	for i := 0; i < *attempts; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			defer func() { <-sem }()
			sent.Add(1)

			req, _ := http.NewRequest("POST", strings.TrimRight(*gateway, "/")+"/v1/chat/completions", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+*key)
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			text := concat(raw)
			if resp.StatusCode == 200 && strings.TrimSpace(text) == "" {
				empties.Add(1)
				mu.Lock()
				if dump == "" {
					dump = string(raw)
				}
				mu.Unlock()
				fmt.Printf("EMPTY #%d status=%d rawLen=%d\n", n, resp.StatusCode, len(raw))
			}
		}(i)
	}
	wg.Wait()

	fmt.Printf("sent=%d empty200=%d\n", sent.Load(), empties.Load())
	if dump != "" {
		fmt.Println("=== first empty reply, raw SSE ===")
		fmt.Println(dump)
	}
}

func concat(sse []byte) string {
	var out strings.Builder
	for _, line := range strings.Split(string(sse), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if p == "[DONE]" || p == "" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error any `json:"error"`
		}
		if json.Unmarshal([]byte(p), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			out.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return out.String()
}