package chathub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	rs          = "\x1e"
	defaultTone = "magic"
	wsBase      = "wss://substrate.office.com/m365Copilot/Chathub"
)

// Variants mirrored from the verified browser / Python probe.
const variants = "EnableMcpServerWidgets,feature.EnableMcpServerWidgets,feature.EnableLuForChatCIQ,feature.enableChatCIQPlugin,EnableRequestPlugins,feature.EnableSensitivityLabels,EnableUnsupportedUrlDetector,feature.IsCustomEngineCopilotEnabled,feature.bizchatfluxv3,feature.enablechatpages,feature.enableCodeCanvas,feature.turnOnWorkTabRecommendation,turnOffWorkTabUpsellFromClient,feature.turnOnDARecommendation,feature.IsStreamingModeInChatRequestEnabled,IncludeSourceAttributionsConcise,SkipPublishEmptyMessage,feature.EnableDeduplicatingSourceAttributions,Enable3PActionProgressMessages,feature.enableClientWebRtc,feature.EnableMeetingRecapOfSeriesMeetingWithCiq,feature.EnableReferencesListCompleteSignal,feature.StorageMessageSplitDisabled,feature.EnableCuaTakeControlApi,feature.cwcallowedos,feature.disabledisallowedmsgs,feature.enableCitationsForSynthesisData,feature.enableGenerateGraphicArtOptionsSet,cdximagen,feature.EnableUpdatedUXForConfirmationDialog,feature.EnableClientFileURLSupportForOfficeWebPaidCopilot,feature.EnableDesignEditorImageGrounding,feature.EnableDesignerEditor,feature.OfficeWebToHelix,feature.OfficeDesktopToHelix,feature.M365TeamsHubToHelix,feature.OwaHubToHelix,feature.MonarchHubToHelix,feature.Win32OutlookHubToHelix,feature.MacOutlookHubToHelix,Agt_bizchat_enableGpt5ForHelix"

type Account struct {
	AccessToken string
	OID         string
	TID         string
}

type Request struct {
	Text           string
	Tone           string
	ConversationID string
	SessionID      string
	Attachments    []Attachment
	Tools          []Tool
	ToolChoice     any
	// Started is true only for the first turn of a ChatHub conversation.
	Started bool
}

// StreamEvent is the protocol-neutral event exposed while ChatHub is still
// producing a response. Text events are safe to show immediately; progress and
// tool events are normally buffered by protocol adapters.
type StreamEvent struct {
	Kind        string
	Text        string
	MessageType string
	ContentType string
	ToolName    string
	Arguments   json.RawMessage
	Raw         json.RawMessage
}

type StreamHandler func(StreamEvent) error

type Result struct {
	Text           string
	ConversationID string
	SessionID      string
	RequestID      string
	Throttling     any
	RawResult      string
	Events         []json.RawMessage
	Normalized     []Event
	Images         []string
}

type Client struct {
	HTTPHeader http.Header
	Dialer     *websocket.Dialer
}

func NewClient() *Client {
	h := make(http.Header)
	h.Set("Origin", "https://m365.cloud.microsoft")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0")
	return &Client{
		HTTPHeader: h,
		Dialer: &websocket.Dialer{
			HandshakeTimeout: 20 * time.Second,
			// substrate frames can be large
			ReadBufferSize:  1024 * 1024,
			WriteBufferSize: 64 * 1024,
		},
	}
}

func (c *Client) Chat(ctx context.Context, acc Account, req Request) (Result, error) {
	return c.ChatWithDelta(ctx, acc, req, nil)
}

// ChatWithEvents is the compatibility entry point for the full event stream.
// The initial implementation exposes every upstream text delta immediately;
// the existing ChatWithDelta path remains the source of truth until the
// SignalR frame parser is migrated to emit progress/tool events as well.
func (c *Client) ChatWithEvents(ctx context.Context, acc Account, req Request, handler StreamHandler) (Result, error) {
	return c.chatWithHandlers(ctx, acc, req, func(text string) error {
		if handler == nil {
			return nil
		}
		return handler(StreamEvent{Kind: "text", Text: text})
	}, handler)
}

// ChatWithDelta preserves Chat semantics while exposing upstream text deltas as
// soon as SignalR delivers them. onDelta must return quickly; returning an error
// cancels the request. Full snapshot messages are retained for final-result
// reconstruction but are not emitted as deltas, preventing duplicate text.
func (c *Client) ChatWithDelta(ctx context.Context, acc Account, req Request, onDelta func(string) error) (Result, error) {
	return c.chatWithHandlers(ctx, acc, req, onDelta, nil)
}

func (c *Client) chatWithHandlers(ctx context.Context, acc Account, req Request, onDelta func(string) error, onEvent StreamHandler) (Result, error) {
	if acc.AccessToken == "" || acc.OID == "" || acc.TID == "" {
		return Result{}, fmt.Errorf("missing access token / oid / tid")
	}
	if strings.TrimSpace(req.Text) == "" {
		return Result{}, fmt.Errorf("empty prompt")
	}
	if req.Tone == "" {
		req.Tone = defaultTone
	}
	firstTurn := req.Started
	if req.SessionID == "" {
		req.SessionID = uuid.NewString()
		firstTurn = true
	}
	if req.ConversationID == "" {
		req.ConversationID = uuid.NewString()
		firstTurn = true
	}
	requestID := uuid.NewString()

	wsURL, err := buildWSURL(acc, req.SessionID, req.ConversationID, requestID)
	if err != nil {
		return Result{}, err
	}

	conn, _, err := c.Dialer.DialContext(ctx, wsURL, c.HTTPHeader.Clone())
	if err != nil {
		return Result{}, fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"protocol":"json","version":1}`+rs)); err != nil {
		return Result{}, fmt.Errorf("handshake send: %w", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		return Result{}, fmt.Errorf("handshake recv: %w", err)
	}

	payload := chatPayload(req.Text, req.SessionID, req.ConversationID, requestID, req.Tone, firstTurn, req.Attachments, req.Tools, req.ToolChoice)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		return Result{}, fmt.Errorf("chat send: %w", err)
	}

	var deltas []string
	var streamedText string
	emitDelta := func(d string) error {
		if d == "" {
			return nil
		}
		streamedText += d
		if onDelta != nil {
			return onDelta(d)
		}
		return nil
	}
	emitSnapshot := func(snapshot string) error {
		if snapshot == "" {
			return nil
		}
		if streamedText != "" && strings.HasPrefix(snapshot, streamedText) {
			return emitDelta(strings.TrimPrefix(snapshot, streamedText))
		}
		return emitDelta(snapshot)
	}
	var final string
	var throttling any
	var rawResult string
	var events []json.RawMessage
	seenStreamTools := map[string]bool{}

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Never convert a timeout or dropped WebSocket into a successful
			// partial response. A response is complete only after SignalR type 3.
			return Result{}, fmt.Errorf("ws read before completion: %w", err)
		}
		for _, part := range strings.Split(string(msg), rs) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			events = append(events, json.RawMessage(append([]byte(nil), part...)))
			var obj map[string]any
			if err := json.Unmarshal([]byte(part), &obj); err != nil {
				continue
			}
			t, _ := obj["type"].(float64)
			target, _ := obj["target"].(string)

			// SignalR ping
			if int(t) == 6 {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":6}`+rs))
				continue
			}

			if int(t) == 1 && target == "update" {
				args, _ := obj["arguments"].([]any)
				for _, raw := range args {
					arg, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					msgs, _ := arg["messages"].([]any)
					if onEvent != nil {
						for _, ev := range extractToolEvents(arg, seenStreamTools) {
							if err := onEvent(ev); err != nil {
								return Result{}, err
							}
						}

						for _, ev := range classifyUpdateMessages(msgs) {
							ev.Raw = eventRaw(arg)
							if ev.Kind != "text" {
								if err := onEvent(ev); err != nil {
									return Result{}, err
								}
							}
						}
					}
					toolFrame := false
					for _, mraw := range msgs {
						m, _ := mraw.(map[string]any)
						mt, _ := m["messageType"].(string)
						ct, _ := m["contentType"].(string)
						if mt == "Progress" || ct == "SearchResults" || ct == "Code" || ct == "ToolCall" {
							toolFrame = true
						}
					}
					if w, ok := arg["writeAtCursor"].(string); ok && w != "" && !toolFrame {
						deltas = append(deltas, w)
						if err := emitSnapshot(w); err != nil {
							return Result{}, err
						}
					}
					if thr, ok := arg["throttling"]; ok {
						throttling = thr
					}
					if msgs, ok := arg["messages"].([]any); ok {
						for _, mraw := range msgs {
							m, ok := mraw.(map[string]any)
							if !ok {
								continue
							}
							author, _ := m["author"].(string)
							text, _ := m["text"].(string)
							mt, _ := m["messageType"].(string)
							if author == "bot" && mt == "" && text != "" {
								// ChatHub often sends the first visible text as a full snapshot,
								// followed by cursor deltas. Emit only the unseen suffix.
								deltas = append(deltas, text)
								if err := emitSnapshot(text); err != nil {
									return Result{}, err
								}
							}
						}
					}
				}
				continue
			}

			if int(t) == 2 {
				item, _ := obj["item"].(map[string]any)
				if item != nil {
					if thr, ok := item["throttling"]; ok {
						throttling = thr
					}
					if res, ok := item["result"].(map[string]any); ok {
						rawResult, _ = res["value"].(string)
						if msg, ok := res["message"].(string); ok {
							final = msg
						}
					}
				}
				// completion frame often follows; keep reading a bit but we already have content
				continue
			}

			if int(t) == 3 {
				if errObj, ok := obj["error"].(map[string]any); ok {
					return Result{}, fmt.Errorf("chathub completion error: %v", errObj)
				}
				// end of stream
				text := final
				if text == "" {
					text = strings.Join(deltas, "")
				}
				return Result{
					Text:           text,
					ConversationID: req.ConversationID,
					SessionID:      req.SessionID,
					RequestID:      requestID,
					Throttling:     throttling,
					RawResult:      rawResult,
					Events:         events,
					Normalized:     NormalizeEvents(events),
					Images:         imageURLs(events),
				}, nil
			}
		}
	}

	// Reaching the overall deadline without a SignalR completion frame is
	// an incomplete upstream response. Do not return accumulated deltas as if
	// they were a successful, finished answer.
	return Result{}, fmt.Errorf("chathub response deadline exceeded before completion")
}

func buildWSURL(acc Account, sessionID, conversationID, requestID string) (string, error) {
	q := url.Values{}
	q.Set("chatsessionid", requestID)
	q.Set("clientrequestid", requestID)
	q.Set("X-SessionId", sessionID)
	q.Set("ConversationId", conversationID)
	q.Set("access_token", acc.AccessToken)
	q.Set("variants", variants)
	// source must keep quotes like the browser probe
	q.Set("source", `"officeweb"`)
	q.Set("product", "Office")
	q.Set("agentHost", "Bizchat.FullScreen")
	q.Set("licenseType", "Starter")
	q.Set("agent", "web")
	q.Set("scenario", "OfficeWebIncludedCopilot")

	// url.Values encodes quotes; probe used safe='",' so keep quotes unescaped-ish.
	// Gorilla/url will encode " to %22 which MS accepts.
	u := fmt.Sprintf("%s/%s@%s?%s", wsBase, acc.OID, acc.TID, q.Encode())
	return u, nil
}

func chatPayload(text, sessionID, conversationID, requestID, tone string, firstTurn bool, attachments []Attachment, tools []Tool, toolChoice any) string {
	text = toolProtocolPrompt(text, tools, toolChoice)
	chat := map[string]any{
		"arguments": []any{
			map[string]any{
				"source":              "officeweb",
				"clientCorrelationId": uuid.NewString(),
				"sessionId":           sessionID,
				"optionsSets":         []any{},
				"options":             map[string]any{},
				"allowedMessageTypes": []string{
					"Chat", "EndOfRequest",
				},
				"sliceIds":          []any{},
				"threadLevelGptId":  map[string]any{},
				"conversationId":    conversationID,
				"traceId":           uuid.NewString(),
				"isStartOfSession":  firstTurn,
				"productThreadType": "Office",
				"clientInfo": map[string]any{
					"clientPlatform": "mcmcopilot-web",
					"clientAppName":  "Office",
				},
				"tone":          tone,
				"streamingMode": "ConciseWithPadding",
				"message": map[string]any{
					"author":      "user",
					"attachments": attachments,
					"inputMethod": "Keyboard",
					"text":        text,
					"requestId":   requestID,
					"locationInfo": map[string]any{
						"timeZoneOffset": 8,
						"timeZone":       "Asia/Shanghai",
					},
					"locale":         "en-US",
					"messageType":    "Chat",
					"experienceType": "Default",
				},
				"plugins":    clientPlugins(tools),
				"toolChoice": toolChoice,
			},
		},
		"invocationId": "0",
		"target":       "chat",
		"type":         4,
	}
	metrics := map[string]any{
		"arguments": []any{
			map[string]any{
				"Timestamps": map[string]string{
					"ConnectionStart":       "",
					"UserInputStart":        "",
					"ConnectionEstablished": "",
					"UserInputSubmit":       "",
				},
			},
		},
		"target": "Metrics",
		"type":   1,
	}
	b1, _ := json.Marshal(chat)
	b2, _ := json.Marshal(metrics)
	return string(b1) + rs + string(b2) + rs
}
