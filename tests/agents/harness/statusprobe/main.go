// StatusProbe reports the status code and body of every non-200 response so a
// failure rate under load can be attributed to a specific cause.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	gateway := flag.String("gateway", "http://127.0.0.1:4141", "gateway base URL")
	key := flag.String("key", "", "api key")
	c := flag.Int("c", 30, "concurrency")
	n := flag.Int("n", 60, "requests")
	flag.Parse()

	payload := map[string]any{
		"model":      "gpt-5.6-sol",
		"stream":     false,
		"max_tokens": 400,
		"messages":   []any{map[string]any{"role": "user", "content": "Explain quicksort with a python code block and explain it line by line."}},
	}
	b, _ := json.Marshal(payload)

	client := &http.Client{
		Timeout:   240 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128},
	}

	var ok, bad atomic.Int64
	byCode := map[string]int{}
	var samples []string
	var mu sync.Mutex
	sem := make(chan struct{}, *c)
	var wg sync.WaitGroup

	for i := 0; i < *n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			req, _ := http.NewRequest("POST", strings.TrimRight(*gateway, "/")+"/v1/chat/completions", bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+*key)
			resp, err := client.Do(req)
			if err != nil {
				mu.Lock()
				byCode["transport:"+err.Error()]++
				mu.Unlock()
				bad.Add(1)
				return
			}
			buf := new(bytes.Buffer)
			buf.ReadFrom(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ok.Add(1)
				return
			}
			bad.Add(1)
			body := buf.String()
			code, msg := parse(body)
			key2 := fmt.Sprintf("%d %s", resp.StatusCode, code)
			mu.Lock()
			byCode[key2]++
			if len(samples) < 6 {
				samples = append(samples, fmt.Sprintf("%d %s :: %s", resp.StatusCode, code, msg))
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	fmt.Printf("ok=%d bad=%d of %d\n", ok.Load(), bad.Load(), *n)
	for k, v := range byCode {
		fmt.Printf("  %-55s %d\n", k, v)
	}
	for _, s := range samples {
		fmt.Printf("  sample: %s\n", s)
	}
	if bad.Load() > 0 {
		os.Exit(1)
	}
}

func parse(body string) (errCode, errMsg string) {
	var v struct {
		Err struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &v) == nil && v.Err.Message != "" {
		return v.Err.Code, v.Err.Message
	}
	if len(body) > 160 {
		body = body[:160]
	}
	return "unparsed", body
}