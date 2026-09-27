package web

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// chromeUA 与 chathub 包 WS 侧保持一致，避免 WS 与 REST 指纹混搭（HAR 报告 08 §3）。
const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const healthPingEndpoint = "https://m365.cloud.microsoft/client_health_ping"

const healthPingTTL = 6 * time.Hour

var (
	healthPingMu   sync.Mutex
	healthPingSeen = map[string]time.Time{}
)

func init() {
	go func() {
		for {
			time.Sleep(30 * time.Minute)
			healthPingMu.Lock()
			now := time.Now()
			for k, v := range healthPingSeen {
				if now.Sub(v) > healthPingTTL {
					delete(healthPingSeen, k)
				}
			}
			healthPingMu.Unlock()
		}
	}()
}

// healthPing 复刻浏览器页面加载期固定顺序的三条健康探针
// （documentLoad → loggerInit → mainLoad）。缺失它们会形成
// “有 chat WS 流量但零遥测”的聚类异常（HAR 报告 08 §4）。
// 每个账号每 healthPingTTL 至多触发一次，异步执行不阻塞请求。
func (s *Server) healthPing(accountID, accessToken, oid, tid, sessionID, requestID string) {
	if accountID == "" || accessToken == "" || oid == "" || tid == "" {
		return
	}
	healthPingMu.Lock()
	if last, ok := healthPingSeen[accountID]; ok && time.Since(last) < healthPingTTL {
		healthPingMu.Unlock()
		return
	}
	healthPingSeen[accountID] = time.Now()
	healthPingMu.Unlock()

	if sessionID == "" {
		sessionID = requestID
	}
	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		for _, kind := range []string{"documentLoad", "loggerInit", "mainLoad"} {
			q := url.Values{}
			q.Set("type", kind)
			q.Set("route", "/chat")
			q.Set("traceId", requestID)
			q.Set("sessionId", sessionID)
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthPingEndpoint+"?"+q.Encode(), nil)
			if err != nil {
				return
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			req.Header.Set("x-anchormailbox", "Oid:"+oid+"@"+tid)
			req.Header.Set("User-Agent", chromeUA)
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("[health-ping] type=%s err=%v", kind, err)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			log.Printf("[health-ping] type=%s status=%d", kind, resp.StatusCode)
		}
	}()
}
