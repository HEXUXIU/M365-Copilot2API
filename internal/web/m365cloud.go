package web

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type M365CloudClient struct {
	mu           sync.Mutex
	clientID     string
	tenantID     string
	refreshToken string
	accessToken  string
	expiresAt    time.Time
	httpClient   *http.Client
}

func NewM365CloudClient(clientID, tenantID, refreshToken string) *M365CloudClient {
	return &M365CloudClient{
		clientID:     clientID,
		tenantID:     tenantID,
		refreshToken: refreshToken,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *M365CloudClient) updateRefreshToken(newToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if newToken != "" && newToken != c.refreshToken {
		c.refreshToken = newToken
	}
}

func (c *M365CloudClient) getAccessToken() (string, error) {
	c.mu.Lock()
	if c.accessToken != "" && time.Now().Before(c.expiresAt.Add(-2*time.Minute)) {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.expiresAt.Add(-2*time.Minute)) {
		return c.accessToken, nil
	}

	v := url.Values{}
	v.Set("client_id", c.clientID)
	v.Set("refresh_token", c.refreshToken)
	v.Set("grant_type", "refresh_token")
	v.Set("scope", "https://m365.cloud.microsoft/v2/.default")
	payload := v.Encode()

	resp, err := c.httpClient.Post(
		fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", c.tenantID),
		"application/x-www-form-urlencoded",
		io.NopCloser(stringReader(payload)),
	)
	if err != nil {
		return "", fmt.Errorf("token refresh: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("token error: %s - %s", result.Error, result.ErrorDesc)
	}

	c.accessToken = result.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	if result.RefreshToken != "" {
		c.refreshToken = result.RefreshToken
	}

	log.Printf("[m365-cloud] token refreshed, expires in %ds", result.ExpiresIn)
	return c.accessToken, nil
}

func (c *M365CloudClient) doAPI(action string, payload map[string]any) (map[string]any, error) {
	token, err := c.getAccessToken()
	if err != nil {
		return nil, err
	}

	reqBody := map[string]any{
		"action": action,
		"state":  payload,
	}
	for k, v := range payload {
		if k != "state" {
			reqBody[k] = v
		}
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://m365.cloud.microsoft/chat", io.NopCloser(stringReader(string(jsonBody))))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0")
	req.Header.Set("Origin", "https://m365.cloud.microsoft")
	req.Header.Set("Referer", "https://m365.cloud.microsoft/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryAfter, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		bodySnippet := ""
		if b, rerr := io.ReadAll(io.LimitReader(resp.Body, 512)); rerr == nil {
			bodySnippet = string(b)
		}
		return nil, &UpstreamHTTPError{
			Status:     resp.StatusCode,
			RetryAfter: retryAfter,
			Body:       bodySnippet,
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return nil, fmt.Errorf("unexpected content type from m365 endpoint: %s", resp.Header.Get("Content-Type"))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse m365 response: %w", err)
	}

	return result, nil
}

func (c *M365CloudClient) DeleteConversation(conversationID string) error {
	log.Printf("[m365-cloud] deleting conversation %s", conversationID)
	_, err := c.doAPI("DeleteConversation", map[string]any{
		"conversationId": conversationID,
		"state": map[string]any{
			"conversationPageHistoryList": map[string]any{
				"chats": []any{},
			},
		},
	})
	return err
}

func (c *M365CloudClient) ListConversations() ([]map[string]any, error) {
	result, err := c.doAPI("RefreshNavPane", map[string]any{})
	if err != nil {
		return nil, err
	}

	store, ok := result["store"].(map[string]any)
	if !ok {
		log.Printf("[m365-cloud] unexpected response: %v", result)
		return nil, fmt.Errorf("unexpected response format")
	}

	historyList, ok := store["conversationPageHistoryList"].(map[string]any)
	if !ok {
		log.Printf("[m365-cloud] conversationPageHistoryList missing from store, returning empty list. store keys: %v", func() []string {
			keys := make([]string, 0)
			for k := range store {
				keys = append(keys, k)
			}
			return keys
		}())
		return []map[string]any{}, nil
	}

	chatsRaw, ok := historyList["chats"].([]any)
	if !ok {
		log.Printf("[m365-cloud] chats type: %T, value: %v", historyList["chats"], historyList["chats"])
		return nil, fmt.Errorf("no chats")
	}

	chats := make([]map[string]any, 0, len(chatsRaw))
	for _, raw := range chatsRaw {
		switch v := raw.(type) {
		case string:
			var chat map[string]any
			if err := json.Unmarshal([]byte(v), &chat); err != nil {
				log.Printf("[m365-cloud] failed to parse chat string: %v", err)
				continue
			}
			chats = append(chats, chat)
		case map[string]any:
			chats = append(chats, v)
		default:
			log.Printf("[m365-cloud] unexpected chat type: %T", raw)
		}
	}

	return chats, nil
}

func (c *M365CloudClient) CleanupOldConversations(maxAge time.Duration, keepN int) (int, error) {
	// 微软历史列表是"滑动式"的：RefreshNavPane 一次只返回一屏对话，
	// 删除后进行到的对话会顶上来成为新一批。因此循环拉取删除，直到列表清空。
	now := time.Now().UnixMilli()
	deleted := 0
	kept := 0
	for round := 0; round < 100; round++ {
		chats, err := c.ListConversations()
		if err != nil {
			return deleted, err
		}
		if len(chats) == 0 {
			break
		}
		anyDeleted := false
		for _, chat := range chats {
			convID, _ := chat["conversationId"].(string)
			createTime, ok := chat["createTimeUtc"].(float64)
			if convID == "" {
				continue
			}
			if !ok {
				continue
			}

			age := time.Duration(now-int64(createTime)) * time.Millisecond
			if age > maxAge {
				if err := c.DeleteConversation(convID); err != nil {
					log.Printf("[m365-cloud] failed to delete %s: %v", convID, err)
					continue
				}
				deleted++
				anyDeleted = true
			} else {
				if kept >= keepN {
					if err := c.DeleteConversation(convID); err != nil {
						log.Printf("[m365-cloud] failed to delete %s: %v", convID, err)
						continue
					}
					deleted++
					anyDeleted = true
				} else {
					kept++
				}
			}
		}
		// 本轮没有任何删除（剩余的都是保留项），列表不会再变化，停止循环。
		if !anyDeleted {
			break
		}
	}

	log.Printf("[m365-cloud] cleanup: deleted %d, kept %d", deleted, kept)
	return deleted, nil
}

type stringReader string

func (s stringReader) Read(p []byte) (int, error) {
	n := copy(p, s)
	if n >= len(s) {
		return n, io.EOF
	}
	return n, nil
}

var m365CloudClient *M365CloudClient

func InitM365CloudClient(clientID, tenantID, refreshToken string) {
	m365CloudClient = NewM365CloudClient(clientID, tenantID, refreshToken)
}

func GetM365CloudClient() *M365CloudClient {
	return m365CloudClient
}
