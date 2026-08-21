package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	apiKey := "m365_9b7a656d5c03921308cafc946db8a760f475b33e715824e7d4021b5b7ba2dbf0"
	baseURL := "http://127.0.0.1:4142"

	fmt.Println("=== SSE Connect ===")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/mcp/sse", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SSE connect failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Printf("SSE status: %d\n", resp.StatusCode)
	fmt.Printf("SSE headers: %v\n", resp.Header)

	scanner := bufio.NewScanner(resp.Body)
	sessionID := ""
	gotEndpoint := false

	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("SSE line: %q\n", line)
		if strings.Contains(line, "sessionId=") {
			data := line
			if idx := strings.Index(data, "sessionId="); idx >= 0 {
				sessionID = data[idx+10:]
				if amp := strings.IndexByte(sessionID, '&'); amp >= 0 {
					sessionID = sessionID[:amp]
				}
			}
			gotEndpoint = true
			break
		}
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
			gotEndpoint = true
		}
	}

	if !gotEndpoint {
		fmt.Println("FAIL: no endpoint event received from SSE")
		remaining, _ := io.ReadAll(resp.Body)
		if len(remaining) > 0 {
			fmt.Printf("Remaining body: %s\n", string(remaining))
		}
		os.Exit(1)
	}

	fmt.Printf("Session ID: %s\n", sessionID)
	if sessionID == "" {
		fmt.Println("WARN: got SSE data but no session ID parsed")
	}
	fmt.Println("\n=== SSE Connect SUCCESS ===")

	// Test initialize via message endpoint
	fmt.Println("\n=== Initialize via Message ===")
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mcpcheck","version":"0.1"}}}`
	msgReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/mcp/message?sessionId="+sessionID, strings.NewReader(initBody))
	msgReq.Header.Set("Content-Type", "application/json")
	msgReq.Header.Set("Authorization", "Bearer "+apiKey)
	msgResp, err := http.DefaultClient.Do(msgReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Message POST failed: %v\n", err)
	} else {
		body, _ := io.ReadAll(msgResp.Body)
		msgResp.Body.Close()
		fmt.Printf("Message status: %d, body: %s\n", msgResp.StatusCode, string(body))
	}

	// Read SSE for initialize response
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("SSE init response: %q\n", line)
		if strings.Contains(line, "instructions") {
			fmt.Println(">>> instructions field FOUND!")
		}
		if strings.Contains(line, "resources") {
			fmt.Println(">>> resources capability FOUND!")
		}
		if strings.HasPrefix(line, "data:") {
			break
		}
	}

	fmt.Println("\n=== Done ===")
}
