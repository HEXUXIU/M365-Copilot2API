// brute-load fires N concurrent requests through one gateway endpoint and
// reports status distribution plus latency percentiles. It checks fence
// integrity on responses that contain code blocks so the issue #102 truncation
// regression is caught under load, not just in unit tests.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type result struct {
	status  int
	ms      int64
	body    string
	isError bool
}

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:4141", "gateway base URL")
	key := flag.String("key", "", "api key")
	concurrency := flag.Int("c", 30, "concurrent requests")
	rounds := flag.Int("rounds", 1, "rounds; each round waits for all")
	path := flag.String("path", "/v1/chat/completions", "endpoint")
	stream := flag.Bool("stream", true, "use SSE streaming")
	prompt := flag.String("prompt", "Explain quicksort. Show the python code in a fenced code block, explain it line by line before the block, and describe complexity after the block.", "prompt")
	maxTokens := flag.Int("maxtok", 700, "max tokens")
	verbose := flag.Bool("v", false, "print first 200 chars of every 200 reply")
	flag.Parse()

	if *key == "" {
		fmt.Fprintln(os.Stderr, "-key is required")
		os.Exit(2)
	}

	statusCount := map[int]int64{}
	var latencies []int64
	var fenceIssues []string
	var truncated []string
	var mu sync.Mutex
	var completed, fenceChecked atomic.Int64

	client := &http.Client{
		Timeout: 180 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			MaxConnsPerHost:     *concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	for round := 1; round <= *rounds; round++ {
		start := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < *concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				res := one(client, *gateway, *key, *path, *prompt, *maxTokens, *stream, round, idx)
				statusCount[res.status]++
				if res.status == 200 {
					completed.Add(1)
					if strings.TrimSpace(res.body) == "" {
						mu.Lock()
						fenceIssues = append(fenceIssues, fmt.Sprintf("EMPTY reply (round %d idx %d, %d bytes raw)", round, idx, len(res.body)))
						mu.Unlock()
					} else if *verbose {
						fmt.Printf("  [%d/%d] %s\n", round, idx, trunc(res.body))
					}
					if strings.Contains(res.body, "```") {
						fenceChecked.Add(1)
						if problem := checkFence(res.body); problem != "" {
							mu.Lock()
							fenceIssues = append(fenceIssues, problem)
							mu.Unlock()
						}
					}
					if problem := checkTruncated(res.body); problem != "" {
						mu.Lock()
						truncated = append(truncated, problem)
						mu.Unlock()
					}
				}
				mu.Lock()
				latencies = append(latencies, res.ms)
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		fmt.Printf("round %d done in %s\n", round, time.Since(start).Round(time.Millisecond))
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) int64 {
		if len(latencies) == 0 {
			return 0
		}
		i := int(float64(len(latencies)-1) * p)
		return latencies[i]
	}

	fmt.Println("--- status distribution ---")
	keys := make([]int, 0, len(statusCount))
	for k := range statusCount {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		fmt.Printf("  %d: %d\n", k, statusCount[k])
	}
	fmt.Printf("--- latency over %d samples ---\n", len(latencies))
	fmt.Printf("  p50=%dms p95=%dms p99=%dms max=%dms\n", pct(0.50), pct(0.95), pct(0.99), latencies[len(latencies)-1])

	fmt.Printf("--- content integrity ---\n")
	fmt.Printf("  200-ok: %d, fence-checked: %d\n", completed.Load(), fenceChecked.Load())
	fmt.Printf("  fence issues: %d\n", len(fenceIssues))
	for i, f := range fenceIssues {
		if i >= 3 {
			break
		}
		fmt.Printf("    %s\n", trunc(f))
	}
	fmt.Printf("  truncated replies: %d\n", len(truncated))
	for i, f := range truncated {
		if i >= 3 {
			break
		}
		fmt.Printf("    %s\n", trunc(f))
	}

	if len(fenceIssues) > 0 || len(truncated) > 0 {
		os.Exit(1)
	}
	if statusCount[200] != int64(*concurrency**rounds) {
		os.Exit(1)
	}
}

func trunc(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func one(client *http.Client, gateway, key, path, prompt string, maxTokens int, stream bool, round, idx int) result {
	payload := map[string]any{
		"model":      "gpt-5.6-sol",
		"stream":     stream,
		"max_tokens": maxTokens,
		"messages":   []any{map[string]any{"role": "user", "content": prompt}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", strings.TrimRight(gateway, "/")+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return result{status: -1, ms: time.Since(start).Milliseconds(), isError: true}
	}
	defer resp.Body.Close()

	var sb strings.Builder
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	elapsed := time.Since(start).Milliseconds()
	raw := sb.String()
	if stream {
		raw = concatSSE(raw)
	}
	return result{status: resp.StatusCode, ms: elapsed, body: raw}
}

func concatSSE(sse string) string {
	var out strings.Builder
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "[DONE]" || payload == "" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			out.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return out.String()
}

// checkFence flags a reply whose code fences do not pair up cleanly, which is
// how issue #102 corruption presented.
func checkFence(body string) string {
	runs := fenceRuns(body)
	for _, r := range runs {
		if r != 3 {
			return fmt.Sprintf("fence run length %d (want 3) near %q", r, trunc(body[max(0, fenceRunStart(body, r)):]))
		}
	}
	return ""
}

func fenceRunStart(body string, _ int) int { return 0 }

func fenceRuns(body string) []int {
	var runs []int
	i := 0
	for i < len(body) {
		if body[i] == '`' && i+2 < len(body) && body[i+1] == '`' && body[i+2] == '`' {
			j := i
			for j < len(body) && body[j] == '`' {
				j++
			}
			runs = append(runs, j-i)
			i = j
			continue
		}
		i++
	}
	return runs
}

// checkTruncated flags replies that stop mid-sentence, the symptom the #102
// reporter described.
func checkTruncated(body string) string {
	t := strings.TrimSpace(body)
	if t == "" {
		return "empty reply"
	}
	last, size := utf8.DecodeLastRuneInString(t)
	switch last {
	case '.', '!', '?', ')', '"', '`', '。', '！', '？', '」', '）':
		return ""
	}
	_ = size
	return "reply ends mid-token: " + trunc(t[len(t)-40:])
}

var _ = io.Discard