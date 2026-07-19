package chathub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"m365-native/internal/outbound"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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
	HTTPClient *http.Client
	Dialer     *websocket.Dialer
	// Trace receives attachment-only metadata; URL contents are never exposed.
	Trace func(map[string]any)
}

func NewClient() *Client {
	h := make(http.Header)
	h.Set("Origin", "https://m365.cloud.microsoft")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0")
	return &Client{
		HTTPHeader: h,
		HTTPClient: outbound.HTTPClient(),
		Dialer:     outbound.WebSocketDialer(),
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
	if strings.TrimSpace(req.Text) == "" && len(req.Attachments) == 0 {
		return Result{}, fmt.Errorf("empty prompt and no attachments")
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
	if err := c.uploadAttachments(ctx, acc, req.ConversationID, req.Attachments); err != nil {
		return Result{}, fmt.Errorf("upload attachment: %w", err)
	}

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
	if c.Trace != nil {
		meta := map[string]any{"stage": "chathub_payload", "attachment_count": len(req.Attachments), "payload_has_attachments": strings.Contains(payload, `"attachments"`), "attachments": []map[string]any{}}
		for _, a := range req.Attachments {
			meta["attachments"] = append(meta["attachments"].([]map[string]any), map[string]any{"type": a.Type, "mime_type": a.MimeType, "url_length": len(a.URL), "data_url": strings.HasPrefix(a.URL, "data:"), "name": a.Name})
		}
		c.Trace(meta)
	}
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

func (c *Client) uploadAttachments(ctx context.Context, acc Account, conversationID string, attachments []Attachment) error {
	for i := range attachments {
		a := &attachments[i]
		if a.Type != "image" || !strings.HasPrefix(a.URL, "data:") {
			continue
		}
		comma := strings.IndexByte(a.URL, ',')
		if comma < 0 {
			return fmt.Errorf("invalid image data URL")
		}
		encoded := a.URL[comma+1:]
		if strings.Contains(strings.ToLower(a.URL[:comma]), ";base64") == false {
			return fmt.Errorf("image URL is not base64")
		}
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return fmt.Errorf("decode image: %w", err)
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("scenario", "UploadImage")
		_ = mw.WriteField("conversationId", conversationID)
		// The browser sends the complete data URL in FileBase64, including the
		// media-type prefix. UploadFile accepts this form and returns docId.
		_ = mw.WriteField("FileBase64", a.URL)
		if c.Trace != nil {
			c.Trace(map[string]any{"stage": "upload_start", "index": i, "conversation_id": conversationID, "mime_type": a.MimeType, "base64_length": len(encoded), "token_present": acc.AccessToken != ""})
		}
		_ = mw.WriteField("optionsSets", "cwcgptvsan")
		_ = mw.WriteField("optionsSets", "flux_v3_gptv_enable_upload_multi_image_in_turn_wo_ch")
		_ = mw.WriteField("optionsSets", "gptvnorm2048")
		if err := mw.Close(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://substrate.office.com/m365Copilot/UploadFile", &body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if acc.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+acc.AccessToken)
		}
		req.Header.Set("Accept", "application/json")
		// Required by the enterprise Copilot UploadFile image-input path.
		// This feature gate is documented in the prior reverse-proxy research
		// and mirrors the PyRIT request flow.
		req.Header.Set("X-Variants", "feature.EnableImageSupportInUploadFile")
		req.Header.Set("X-Scenario", "OfficeWebIncludedCopilot")
		if acc.OID != "" && acc.TID != "" {
			req.Header.Set("X-AnchorMailbox", "Oid:"+acc.OID+"@"+acc.TID)
		}
		for k, vv := range c.HTTPHeader {
			for _, v := range vv {
				if k != "Origin" || v != "" {
					req.Header.Add(k, v)
				}
			}
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if c.Trace != nil {
				c.Trace(map[string]any{"stage": "upload_http_error", "error": err.Error()})
			}
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if c.Trace != nil {
				c.Trace(map[string]any{"stage": "upload_http_status", "status": resp.StatusCode, "response": strings.TrimSpace(string(data[:minInt(len(data), 500)]))})
			}
			return fmt.Errorf("upload status %s: %s", resp.Status, strings.TrimSpace(string(data)))
		}
		var out struct {
			DocID    string `json:"docId"`
			FileName string `json:"fileName"`
			FileType string `json:"fileType"`
			Result   struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("upload response: %w", err)
		}
		if out.Result.Value != "Success" || out.DocID == "" {
			if c.Trace != nil {
				c.Trace(map[string]any{"stage": "upload_failed", "status": resp.StatusCode, "response": string(data[:func() int {
					if len(data) < 500 {
						return len(data)
					}
					return 500
				}()])})
			}
			return fmt.Errorf("upload failed: %s", strings.TrimSpace(string(data)))
		}
		a.DocID = out.DocID
		a.FileType = strings.TrimPrefix(strings.ToLower(out.FileType), ".")
		// ChatHub's ImageFile annotation uses jpg for JPEG uploads.
		if a.FileType == "jpeg" {
			a.FileType = "jpg"
		}
		if a.Name == "" {
			a.Name = out.FileName
		}
		if c.Trace != nil {
			c.Trace(map[string]any{"stage": "upload_success", "doc_id": a.DocID, "file_name": a.Name, "file_type": a.FileType})
		}
	}
	return nil
}

func chatPayload(text, sessionID, conversationID, requestID, tone string, firstTurn bool, attachments []Attachment, tools []Tool, toolChoice any) string {
	text = toolProtocolPrompt(text, tools, toolChoice)
	message := map[string]any{
		"author":                "user",
		"attachments":           attachments,
		"inputMethod":           "Keyboard",
		"text":                  text,
		"entityAnnotationTypes": []string{"People", "File", "Event", "Email", "TeamsMessage"},
		"requestId":             requestID,
		"locationInfo": map[string]any{
			"timeZoneOffset": 8,
			"timeZone":       "Asia/Shanghai",
		},
		"locale":            "zh-cn",
		"messageType":       "Chat",
		"experienceType":    "Default",
		"adaptiveCards":     []any{},
		"clientPreferences": map[string]any{},
	}
	// The browser does not send an OpenAI attachments array to ChatHub. It
	// sends a file annotation after the file has been uploaded by Office.
	annotations := make([]any, 0, len(attachments))
	for _, a := range attachments {
		if a.Type != "image" || a.DocID == "" {
			continue
		}
		if a.Name == "" {
			a.Name = "image." + a.FileType
		}
		fileType := a.FileType
		if fileType == "" {
			fileType = strings.TrimPrefix(strings.ToLower(a.MimeType), "image/")
		}
		if fileType == "" || fileType == "image" || fileType == "*" {
			fileType = "jpg"
		}
		annotations = append(annotations, map[string]any{
			"id": a.DocID,
			"messageAnnotationMetadata": map[string]any{
				"@type": "File", "annotationType": "File",
				"fileType": fileType, "fileName": a.Name,
			},
			"messageAnnotationType": "ImageFile",
		})
	}
	if len(annotations) > 0 {
		message["messageAnnotations"] = annotations
		message["connectedFederatedConnections"] = []string{"dummyId"}
	}
	// Restore the old gateway's multimodal injection path. The historical
	// implementation merged imageUrl/imageBase64 directly into message rather
	// than relying solely on the newer attachments array.
	for _, a := range attachments {
		if a.Type != "image" || a.URL == "" {
			continue
		}
		if strings.HasPrefix(a.URL, "data:") {
			if comma := strings.IndexByte(a.URL, ','); comma >= 0 && comma+1 < len(a.URL) {
				message["imageBase64"] = a.URL[comma+1:]
			}
		} else {
			message["imageUrl"] = a.URL
		}
		break
	}
	optionsSets := []any{
		"search_result_progress_messages_with_search_queries",
		"update_textdoc_response_after_streaming",
		"deepleo_networking_timeout_10minutes_canmore",
		"cwc_flux_image",
		"cwc_code_interpreter",
		"cwc_code_interpreter_amsfix",
		"cwcfluxgptv",
		"flux_v3_gptv_enable_upload_multi_image_in_turn_wo_ch",
		"gptvnorm2048",
		"cwc_code_interpreter_citation_fix",
		"code_interpreter_interactive_charts_inline_image",
		"code_interpreter_matplotlib_patching",
		"code_interpreter_interactive_charts",
		"cwc_fileupload_odb",
		"update_memory_plugin",
		"add_custom_instructions",
		"cwc_flux_v3",
		"flux_v3_progress_messages",
		"enable_batch_token_processing",
		"enable_gg_gpt",
	}
	chat := map[string]any{
		"arguments": []any{
			map[string]any{
				"source":              "officeweb",
				"clientCorrelationId": uuid.NewString(),
				"sessionId":           sessionID,
				"optionsSets":         optionsSets,
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
				"message":       message,

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
