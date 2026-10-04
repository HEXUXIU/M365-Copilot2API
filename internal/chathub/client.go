package chathub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"m365-copilot2api/internal/outbound"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ErrRateLimitNotice identifies the human-readable rate-limit response that
// ChatHub sometimes sends through the text channel instead of HTTP 429.
// Callers must independently probe the account before marking it unhealthy.
var ErrRateLimitNotice = errors.New("upstream rate-limit notice")

var ErrEmptyCompletion = errors.New("upstream returned empty completion; tone may be unavailable for this tenant")

var ErrImageLimit = errors.New("upstream image generation daily limit reached")

var ErrOffensiveContent = errors.New("upstream content policy flagged as offensive")

var ErrFirstTokenTimeout = errors.New("upstream first text token timeout")

var contentPolicyPatterns = []string{
	"很抱歉，我无法响应",
	"我很抱歉，我无法响应",
	"很抱歉，我无法",
	"抱歉，我无法",
	"i'm sorry, i can't respond",
	"i'm sorry, i cannot respond",
	"i apologize, i cannot",
	// Copilot's canned "could not answer" fallback. Passing this through as a
	// normal assistant turn hides the real failure from the caller.
	"wasn't able to respond to that",
	"wasn't able to respond",
	"unable to respond to that",
	"something else i can help with",
	"抱歉，我无法回答这个问题",
	"抱歉，我现在无法回答",
}

func IsContentPolicyBlock(text string) bool {
	if len(text) > 300 {
		return false
	}
	low := strings.ToLower(text)
	for _, p := range contentPolicyPatterns {
		if strings.Contains(low, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// DialError carries the HTTP status and optional Retry-After from a failed
// WebSocket dial so the web layer can route it into the correct cooldown.
type DialError struct {
	Status     int
	RetryAfter int
	Kind       string
	cause      error
}

func (e *DialError) Error() string {
	if e.Kind != "" {
		if e.cause != nil {
			return fmt.Sprintf("ws dial: %s upstream %d: %v", e.Kind, e.Status, e.cause)
		}
		return fmt.Sprintf("ws dial: %s upstream %d", e.Kind, e.Status)
	}
	if e.cause != nil {
		return fmt.Sprintf("ws dial: upstream %d: %v", e.Status, e.cause)
	}
	return fmt.Sprintf("ws dial: upstream %d", e.Status)
}

func (e *DialError) Unwrap() error { return e.cause }

func classifyTransportError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "socks"):
		return "SOCKS5"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "no address associated") || strings.Contains(msg, "name resolution") || strings.Contains(msg, "dns"):
		return "DNS"
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		return "TLS"
	case strings.Contains(msg, "handshake"):
		return "WS_HANDSHAKE"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "timeout") && strings.Contains(msg, "read"):
		// A write timeout is not a read timeout: failing to send the payload and
		// failing to receive a reply are different faults to diagnose.
		if strings.Contains(msg, "write") {
			return "WS_WRITE_TIMEOUT"
		}
		return "WS_READ_TIMEOUT"
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "network is unreachable") || strings.Contains(msg, "connection was forcibly closed"):
		return "TCP"
	default:
		if strings.Contains(msg, "timeout") {
			if strings.Contains(msg, "write") {
				return "WS_WRITE_TIMEOUT"
			}
			return "WS_READ_TIMEOUT"
		}
		return "TCP"
	}
}

func wrapDialError(err error, status int, retryAfter int) *DialError {
	kind := ""
	if status == 0 && err != nil {
		if errors.Is(err, context.Canceled) {
			kind = "CLIENT_CANCELED"
		} else if errors.Is(err, context.DeadlineExceeded) {
			kind = "WS_READ_TIMEOUT"
		} else {
			kind = classifyTransportError(err)
		}
	}
	return &DialError{Status: status, RetryAfter: retryAfter, Kind: kind, cause: err}
}

// Phase is the 7-stage state machine for a ChatHub exchange.
// Only PayloadSent is zero-side-effect and retriable; once streaming has
// begun (any text, tool event or reused conversation) failover must not
// switch accounts.
type Phase int

const (
	PhaseInit Phase = iota
	PhaseDial
	PhaseHandshake
	PhaseUpload
	PhasePayloadSent
	PhaseStreaming
	PhaseCompleted
)

func IsRetriablePhase(p Phase) bool {
	return p == PhasePayloadSent
}

var chTrace = os.Getenv("M365_TRACE") == "1"

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const (
	rs          = "\x1e"
	defaultTone = "Magic"
	wsBase      = "wss://substrate.office.com/m365Copilot/Chathub"
	// maxAttachments bounds per-request remote downloads: each image is
	// base64-encoded and held in memory alongside the multipart body.
	maxAttachments   = 10
	maxAttachmentMiB = 10
)

// Variants mirrored from the verified browser / Python probe.
const variants = "EnableMcpServerWidgets,feature.EnableMcpServerWidgets,feature.EnableLuForChatCIQ,feature.enableChatCIQPlugin,EnableRequestPlugins,feature.EnableSensitivityLabels,EnableUnsupportedUrlDetector,feature.IsCustomEngineCopilotEnabled,feature.bizchatfluxv3,feature.enablechatpages,feature.enableCodeCanvas,feature.turnOnDARecommendation,feature.IsStreamingModeInChatRequestEnabled,IncludeSourceAttributionsConcise,SkipPublishEmptyMessage,feature.EnableDeduplicatingSourceAttributions,Enable3PActionProgressMessages,feature.enableClientWebRtc,feature.EnableMeetingRecapOfSeriesMeetingWithCiq,feature.EnableReferencesListCompleteSignal,feature.StorageMessageSplitDisabled,feature.cwcallowedos,feature.disabledisallowedmsgs,feature.enableCitationsForSynthesisData,feature.enableGenerateGraphicArtOptionsSet,cdximagen,feature.EnableUpdatedUXForConfirmationDialog,feature.EnableClientFileURLSupportForOfficeWebPaidCopilot,feature.EnableDesignEditorImageGrounding,feature.EnableDesignerEditor,feature.OfficeWebToHelix,feature.OfficeDesktopToHelix,feature.M365TeamsHubToHelix,feature.OwaHubToHelix,feature.MonarchHubToHelix,feature.Win32OutlookHubToHelix,feature.MacOutlookHubToHelix,Agt_bizchat_enableGpt5ForHelix,feature.EnableImageGenInsufficientTokensThrottled,feature.EnableImageGenSystemCapacityThrottled,feature.EnableConversationShareApis,feature.IsCitationsReferencesOutputEnabled,feature.enableDeltaStreamingForReferences,feature.enableIncludeReferencesInDeltaResponse,feature.enablereferencesforagents,feature.EnableMergingPureDeltas,feature.EnableRemoveStreamingMode,feature.EnableCodeInterpreterConversion,agt_module_attr_enableReferencesForCodeInterpreter,agt_module_enableCodeInterpreterHallucinatedUrlFilter,SingletonEnvOn,cdxenablefccinmainline,EnableComposeWidget,feature.EnableContentApiandDocTypeHtmlInRichAnswers,cdxgrounding_api_v2_rich_web_answers_reference_bottom_force,cdxenablerenderforisocomp,feature.EnableSkipRehydrationForSpeCIdImages,feature.EnablePersonalization,feature.EnableBase64DataInMessageAnnotations,feature.EnableSkipEmittingMessageOnFlush,feature.EnableRemoveEmptySourceAttributions,agt_researcheragent_enableMemoryRead"

type Account struct {
	AccessToken string
	OID         string
	TID         string
}

type Request struct {
	Text                  string
	Tone                  string
	ConversationID        string
	SessionID             string
	Attachments           []Attachment
	Tools                 []Tool
	ToolChoice            any
	MCPServerURL          string
	DisableWebSearch      bool
	Started               bool
	ConversationSignature string
	PreviousMessages      []ContextMessage
	LicenseType           string
	Scenario              string
	ConnectedFederatedIDs []string
	FeatureFlags          FeatureFlags
	DisableMemory         bool
	Locale                string
	Market                string
	TimeZone              string
	TimeZoneOffset        int
	DeviceOS              string
	FirstTokenTimeout     time.Duration
}

type FeatureFlags struct {
	MemoryV2             bool
	DeepWork             bool
	ComputerUse          bool
	RealtimeVoice        bool
	SystemPromptOverride bool
	DesignerImageGen4o   bool
	CodeCanvas           bool
	SydneyReconnect      bool
}

type ContextMessage struct {
	Author      string `json:"author"`
	Description string `json:"description"`
	ContextType string `json:"contextType"`
	MessageType string `json:"messageType"`
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

type Timestamps struct {
	RequestSent                  string `json:"requestSent"`
	FirstServiceResponseReceived string `json:"firstServiceResponseReceived,omitempty"`
	FirstTokenReceived           string `json:"firstTokenReceived,omitempty"`
	LastTokenReceived            string `json:"lastTokenReceived,omitempty"`
}

type Result struct {
	Text                      string
	Reasoning                 string
	ConversationID            string
	SessionID                 string
	RequestID                 string
	Throttling                any
	SuggestedResponses        []SuggestedResponse
	RawResult                 string
	Events                    []json.RawMessage
	Normalized                []Event
	Images                    []string
	Offense                   string
	Scores                    []Score
	ConversationTransferToken string
	MeteringInformation       any
	SpokenText                string
	StorageMessageID          string
	References                map[string]Reference
	Timestamps                Timestamps
}

type SuggestedResponse struct {
	CommandText        string `json:"commandText"`
	Text               string `json:"text"`
	SuggestionCategory string `json:"suggestionCategory,omitempty"`
	ContentOrigin      string `json:"contentOrigin,omitempty"`
	HiddenText         string `json:"hiddenText,omitempty"`
	MessageID          string `json:"messageId,omitempty"`
	Author             string `json:"author,omitempty"`
	CreatedAt          string `json:"createdAt,omitempty"`
	MessageType        string `json:"messageType,omitempty"`
	Offense            string `json:"offense,omitempty"`
}

type Score struct {
	Component string  `json:"component"`
	Score     float64 `json:"score"`
}

type Reference struct {
	TargetLink          string `json:"targetLink"`
	ProviderDisplayName string `json:"providerDisplayName,omitempty"`
	Title               string `json:"title,omitempty"`
	Snippet             string `json:"snippet,omitempty"`
	LastUpdatedDate     string `json:"lastUpdatedDate,omitempty"`
}

type Client struct {
	HTTPHeader http.Header
	HTTPClient *http.Client
	Dialer     *websocket.Dialer
	Pool       *ConnPool
	Trace      func(map[string]any)
}

func NewClient() *Client {
	h := make(http.Header)
	h.Set("Origin", "https://m365.cloud.microsoft")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	d := outbound.WebSocketDialer()
	return &Client{
		HTTPHeader: h,
		HTTPClient: outbound.HTTPClient(),
		Dialer:     d,
		Pool:       NewConnPool(d, h),
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

// ChatWithReasoning is the streaming entry point used by the OpenAI-compatible
// layer. onDelta receives answer text tokens, onReasoning receives the
// multi-step ChainOfThought transcript that ChatHub marks with
// contentOrigin=ChainOfThoughtSummary / addToChainOfThought=true.
func (c *Client) ChatWithReasoning(ctx context.Context, acc Account, req Request, onDelta func(string) error, onReasoning func(string) error) (Result, error) {
	return c.chatWithHandlers(ctx, acc, req, onDelta, func(ev StreamEvent) error {
		if ev.Kind == "reasoning" && ev.Text != "" && onReasoning != nil {
			return onReasoning(ev.Text)
		}
		return nil
	})
}

// poolEligibleFor reports whether a request may take from (and return to)
// the WebSocket connection pool. Only fresh requests without caller-supplied
// conversation/session IDs qualify: pooled sockets carry random URL-bound IDs.
func poolEligibleFor(req Request) bool {
	return req.ConversationID == "" && req.SessionID == ""
}

func (c *Client) chatWithHandlers(ctx context.Context, acc Account, req Request, onDelta func(string) error, onEvent StreamHandler) (Result, error) {
	startedAt := time.Now()
	log.Printf("chathub timing start prompt_len=%d", len(req.Text))
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
	// A pooled WebSocket was dialed with random conversation/session IDs, so
	// it is only safe for a fresh request. Continuing an upstream conversation
	// with mismatched URL-bound IDs completes immediately with no text
	// (community PR #62/#86 finding). Fresh requests reuse; continuations dial.
	// poolEligible also gates Return: only a fresh, healthy, completed request
	// hands its connection back to the pool.
	poolEligible := poolEligibleFor(req)
	if poolEligible {
		req.SessionID = uuid.NewString()
		req.ConversationID = uuid.NewString()
		firstTurn = true
	} else {
		if req.SessionID == "" {
			req.SessionID = uuid.NewString()
			firstTurn = true
		}
		if req.ConversationID == "" {
			req.ConversationID = uuid.NewString()
			firstTurn = true
		}
	}
	requestID := uuid.NewString()
	wsURL, err := BuildWSURLWithOptions(acc, req.SessionID, req.ConversationID, requestID, req.LicenseType, req.Scenario, req.DisableMemory)
	if err != nil {
		return Result{}, err
	}
	phase := PhaseInit
	_ = phase
	attachCh := make(chan error, 1)
	if len(req.Attachments) > 0 {
		go func() { attachCh <- c.uploadAttachments(ctx, acc, req.ConversationID, req.Attachments) }()
	}

	dialStarted := time.Now()
	var conn *websocket.Conn
	var reused bool
	phase = PhaseDial

	if c.Pool != nil {
		if !poolEligible {
			// Continuation requests must dial fresh; skip pool Take entirely.
		} else {
			var poolErr error
			conn, reused, poolErr = c.Pool.Take(ctx, acc.OID, acc.TID, wsURL)
			if poolErr != nil {
				if errors.Is(poolErr, context.Canceled) {
					return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: poolErr}
				}
				return Result{}, wrapDialError(poolErr, 0, 0)
			}
			if reused {
				go func() {
					warmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					warmReqID := uuid.NewString()
					warmSID := uuid.NewString()
					warmCID := uuid.NewString()
					warmURL, _ := BuildWSURL(acc, warmSID, warmCID, warmReqID, req.LicenseType, req.Scenario)
					c.Pool.Warm(warmCtx, acc, warmURL)
				}()
			}
		}
	}
	if conn == nil {
		var resp *http.Response
		conn, resp, err = c.Dialer.DialContext(ctx, wsURL, c.HTTPHeader.Clone())
		if err != nil {
			if resp != nil && (resp.StatusCode == 429 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 503) {
				retryAfter := 0
				if v, _ := strconv.Atoi(resp.Header.Get("Retry-After")); v > 0 {
					retryAfter = v
				}
				log.Printf("chathub ws_dial %d Retry-After=%d", resp.StatusCode, retryAfter)
				kind := ""
				switch resp.StatusCode {
				case 429:
					kind = "QUOTA_429"
				case 503:
					kind = "OVERLOAD_503"
				case 401:
					kind = "AUTH_EXPIRED_401"
				case 403:
					kind = "FORBIDDEN_403"
				}
				return Result{}, &DialError{Status: resp.StatusCode, RetryAfter: retryAfter, Kind: kind, cause: err}
			}
			if errors.Is(err, context.Canceled) {
				return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: err}
			}
			return Result{}, wrapDialError(err, 0, 0)
		}
	}
	if reused {
		log.Printf("chathub timing ws_dial_ms=0 total_ms=%d reused=true (pooled)", time.Since(startedAt).Milliseconds())
	} else {
		log.Printf("chathub timing ws_dial_ms=%d total_ms=%d reused=false", time.Since(dialStarted).Milliseconds(), time.Since(startedAt).Milliseconds())
	}
	phase = PhaseHandshake

	var writeMu sync.Mutex
	wsWrite := func(msgType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(msgType, data)
	}

	returnConn := false
	var readerDone <-chan struct{}
	defer func() {
		if returnConn && conn != nil && c.Pool != nil && poolEligible {
			if readerDone != nil {
				_ = conn.SetReadDeadline(time.Now())
				<-readerDone
			}
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			c.Pool.Return(acc.OID, acc.TID, conn)
		} else if conn != nil {
			conn.Close()
		}
	}()

	phase = PhaseUpload
	if len(req.Attachments) > 0 {
		if attachErr := <-attachCh; attachErr != nil {
			returnConn = false
			return Result{}, fmt.Errorf("upload attachment: %w", attachErr)
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(45 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))

	if !reused {
		if err := wsWrite(websocket.TextMessage, []byte(`{"protocol":"json","version":1}`+rs)); err != nil {
			returnConn = false
			if errors.Is(err, context.Canceled) {
				return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: err}
			}
			return Result{}, &DialError{Status: 0, Kind: "WS_HANDSHAKE", cause: fmt.Errorf("handshake send: %w", err)}
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			returnConn = false
			if errors.Is(err, context.Canceled) {
				return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: err}
			}
			kind := "WS_HANDSHAKE"
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
				kind = "WS_HANDSHAKE"
			}
			return Result{}, &DialError{Status: 0, Kind: kind, cause: fmt.Errorf("handshake recv: %w", err)}
		}
	}

	_ = wsWrite(websocket.TextMessage, []byte(`{"type":6}`+rs))

	payload := chatPayload(req, requestID, firstTurn)
	log.Printf("chathub prompt-trace text=%d tools=%d payload=%d", len(req.Text), len(req.Tools), len(payload))
	if c.Trace != nil {
		meta := map[string]any{"stage": "chathub_payload", "attachment_count": len(req.Attachments), "payload_has_attachments": strings.Contains(payload, `"attachments"`), "attachments": []map[string]any{}}
		for _, a := range req.Attachments {
			meta["attachments"] = append(meta["attachments"].([]map[string]any), map[string]any{"type": a.Type, "mime_type": a.MimeType, "url_length": len(a.URL), "data_url": strings.HasPrefix(a.URL, "data:"), "name": a.Name})
		}
		c.Trace(meta)
	}
	log.Printf("chathub timing handshake_ms=%d", time.Since(dialStarted).Milliseconds())
	payloadSentAt := time.Now()
	ts := Timestamps{RequestSent: payloadSentAt.UTC().Format(time.RFC3339Nano)}
	if err := wsWrite(websocket.TextMessage, []byte(payload)); err != nil {
		returnConn = false
		if errors.Is(err, context.Canceled) {
			return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: err}
		}
		return Result{}, &DialError{Status: 0, Kind: classifyTransportError(err), cause: fmt.Errorf("chat send: %w", err)}
	}
	phase = PhasePayloadSent
	var firstTokenTimer *time.Timer
	var firstTokenTimeout <-chan time.Time
	if req.FirstTokenTimeout > 0 {
		firstTokenTimer = time.NewTimer(req.FirstTokenTimeout)
		firstTokenTimeout = firstTokenTimer.C
		defer firstTokenTimer.Stop()
	}

	var deltas []string
	var streamed strings.Builder
	firstTextTokenReceived := false
	emitDelta := func(d string) error {
		if d == "" {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if chTrace {
			log.Printf("[trace:emitDelta] len=%d streamed=%d preview=%q", len(d), streamed.Len()+len(d), truncate(d, 80))
		}
		if !firstTextTokenReceived && strings.TrimSpace(d) != "" {
			firstTextTokenReceived = true
			if firstTokenTimer != nil {
				firstTokenTimer.Stop()
				firstTokenTimeout = nil
			}
			log.Printf("chathub timing first_delta_ms=%d len=%d", time.Since(payloadSentAt).Milliseconds(), len(d))
			ts.FirstTokenReceived = time.Now().UTC().Format(time.RFC3339Nano)
			phase = PhaseStreaming
		}
		streamed.WriteString(d)
		deltas = append(deltas, d)
		if onDelta != nil {
			return onDelta(d)
		}
		return nil
	}
	// ChatHub signals text either as a full snapshot or as cursor rewrites.
	// Only the portion not already streamed may be emitted; naive prefix
	// checks misfire when upstream rewrites the whole buffer, which duplicated
	// answers (AAA…). Match any overlap and emit the tail.
	// Upstream rate limiting surfaces as a human-readable notice on the text
	// channel instead of an HTTP 429. Detect it before any real content has
	// streamed so the web layer can fail over rather than answer with it.
	// The "throttling" frame itself is per-conversation quota metadata and is
	// NOT a rate-limit signal.
	//
	// CPU note: the length guard rejects most snapshots before running the
	// five substring scans; a long answer never reaches them once streaming.
	quickGuard := func(text string) bool {
		if streamed.Len() != 0 {
			return false
		}
		return len(text) <= 4096
	}
	rateLimited := func(text string) bool {
		if !quickGuard(text) {
			return false
		}
		t := strings.ToLower(text)
		return strings.EqualFold(strings.TrimSpace(text), "Throttled") ||
			strings.Contains(t, "temporarily unable to respond to this volume of requests") ||
			strings.Contains(t, "temporarily unable to respond to this many requests") ||
			strings.Contains(t, "太多请求") ||
			strings.Contains(t, "无法响应这么多请求") ||
			strings.Contains(t, "too many requests") ||
			strings.Contains(t, "please retry") && strings.Contains(t, "later")
	}
	imageLimitDetected := func(text string) bool {
		if !quickGuard(text) {
			return false
		}
		t := strings.ToLower(text)
		return strings.Contains(t, "无法生成更多图像") || strings.Contains(t, "unable to generate more images") || strings.Contains(t, "cannot generate more images today")
	}
	contentPolicyDetected := func(text string) bool {
		if !quickGuard(text) {
			return false
		}
		return IsContentPolicyBlock(text)
	}
	// skippedSnapshots counts non-prefix rewrites dropped by emitSnapshot.
	// The messages[].text channel is the authoritative cumulative stream; the
	// writeAtCursor channel is a redundant per-token feed for the same text.
	// Concatenating both is the source of duplicated public paragraphs and
	// U+FFFD, so snapshots are reconciled once and writeAtCursor is ignored
	// outside tracing (ported from community PR #86, commit 7ddf077).
	skippedSnapshots := 0
	var snapshots snapshotReconciler
	emitSnapshot := func(snapshot string) error {
		if snapshot == "" {
			return nil
		}
		if chTrace {
			log.Printf("[trace:emitSnapshot] accepted=%d snapshot=%d", len(snapshots.last), len(snapshot))
		}
		if imageLimitDetected(snapshot) {
			return ErrImageLimit
		}
		if rateLimited(snapshot) {
			return ErrRateLimitNotice
		}
		if contentPolicyDetected(snapshot) {
			return ErrOffensiveContent
		}
		suffix, ok := snapshots.Apply(snapshot)
		if !ok {
			if snapshots.seen {
				skippedSnapshots++
			}
			if chTrace {
				log.Printf("[trace:emitSnapshot] skip: accepted=%d snapshot=%d (duplicate, shorter, or non-prefix)", len(snapshots.last), len(snapshot))
			}
			return nil
		}
		return emitDelta(suffix)
	}
	var final string
	var throttling any
	var rawResult string
	var events []json.RawMessage
	var suggestions []SuggestedResponse
	seenStreamTools := map[string]bool{}
	var reasoningBuf strings.Builder
	var offense string
	var scores []Score
	var conversationTransferToken string
	var meteringInformation any
	var spokenText string
	var storageMessageID string
	references := make(map[string]Reference)
	var firstServiceResponse bool

	type wsRead struct {
		msg []byte
		err error
	}
	readCh := make(chan wsRead, 8)
	done := make(chan struct{})
	readStopped := make(chan struct{})
	readerDone = readStopped
	defer close(done)
	go func() {
		defer close(readStopped)
		defer close(readCh)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
			_, msg, err := conn.ReadMessage()
			select {
			case readCh <- wsRead{msg: msg, err: err}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var read wsRead
		select {
		case <-firstTokenTimeout:
			returnConn = false
			_ = conn.Close()
			return Result{}, ErrFirstTokenTimeout
		case <-ctx.Done():
			returnConn = false
			_ = conn.Close()
			if errors.Is(ctx.Err(), context.Canceled) {
				return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: ctx.Err()}
			}
			return Result{}, &DialError{Status: 0, Kind: "WS_READ_TIMEOUT", cause: ctx.Err()}
		case r, ok := <-readCh:
			if !ok {
				if ctx.Err() != nil {
					returnConn = false
					if errors.Is(ctx.Err(), context.Canceled) {
						return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: ctx.Err()}
					}
					return Result{}, &DialError{Status: 0, Kind: "WS_READ_TIMEOUT", cause: ctx.Err()}
				}
				returnConn = false
				return Result{}, fmt.Errorf("ws read before completion: %w", io.ErrUnexpectedEOF)
			}
			read = r
		}
		if read.err != nil {
			returnConn = false
			if errors.Is(read.err, context.Canceled) {
				return Result{}, &DialError{Status: 0, Kind: "CLIENT_CANCELED", cause: fmt.Errorf("ws read before completion: %w", read.err)}
			}
			kind := "WS_READ_TIMEOUT"
			if strings.Contains(strings.ToLower(read.err.Error()), "timeout") || errors.Is(read.err, context.DeadlineExceeded) {
				kind = "WS_READ_TIMEOUT"
			} else {
				kind = classifyTransportError(read.err)
				if kind == "TCP" {
					kind = "WS_READ_TIMEOUT"
				}
			}
			return Result{}, &DialError{Status: 0, Kind: kind, cause: fmt.Errorf("ws read before completion: %w", read.err)}
		}
		if !firstServiceResponse {
			firstServiceResponse = true
			ts.FirstServiceResponseReceived = time.Now().UTC().Format(time.RFC3339Nano)
		}
		for _, part := range strings.Split(string(read.msg), rs) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if chTrace {
				log.Printf("[trace:ws] frame_len=%d preview=%q", len(part), truncate(part, 120))
			}
			b := []byte(part)
			events = append(events, json.RawMessage(b))
			var obj map[string]any
			if err := json.Unmarshal(b, &obj); err != nil {
				continue
			}
			t, _ := obj["type"].(float64)
			target, _ := obj["target"].(string)

			// SignalR ping
			if int(t) == 6 {
				_ = wsWrite(websocket.TextMessage, []byte(`{"type":6}`+rs))
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
						beforeTools := len(seenStreamTools)
						for _, ev := range extractToolEvents(arg, seenStreamTools) {
							if err := onEvent(ev); err != nil {
								returnConn = false
								return Result{}, err
							}
						}
						if len(seenStreamTools) > beforeTools {
							phase = PhaseStreaming
						}
					}

					for _, ev := range classifyUpdateMessages(msgs) {
						if ev.Kind == "reasoning" {
							reasoningBuf.WriteString(ev.Text)
						}
						ev.Raw = eventRaw(arg)
						if ev.Kind != "text" && onEvent != nil {
							if err := onEvent(ev); err != nil {
								returnConn = false
								return Result{}, err
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
					if thr, ok := arg["throttling"]; ok {
						throttling = thr
					}
					if ctt, ok := arg["conversationTransferToken"].(string); ok && ctt != "" {
						conversationTransferToken = ctt
					}
					if mi, ok := arg["meteringInformation"]; ok && mi != nil {
						meteringInformation = mi
					}
					if srs, ok := arg["suggestedResponses"].([]any); ok {
						for _, srRaw := range srs {
							sr, ok := srRaw.(map[string]any)
							if !ok {
								continue
							}
							suggestions = append(suggestions, parseSuggestedResponse(sr))
						}
					}
					if w, ok := arg["writeAtCursor"].(string); ok && w != "" && !toolFrame && chTrace {
						log.Printf("[trace:writeAtCursor] ignored redundant delta len=%d snapshot_seen=%t", len(w), snapshots.seen)
					}
					if patches, ok := arg["patches"].([]any); ok {
						for _, praw := range patches {
							p, ok := praw.(map[string]any)
							if !ok {
								continue
							}
							op, _ := p["op"].(string)
							path, _ := p["path"].(string)
							value, _ := p["value"]
							if op != "replace" || path == "" {
								continue
							}
							parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
							if len(parts) < 2 {
								continue
							}
							field := parts[1]
							switch field {
							case "spokenText":
								if vs, ok := value.(string); ok {
									spokenText = vs
								}
							}
						}
					}
					if refs, ok := arg["references"].(map[string]any); ok && len(refs) > 0 {
						for k, v := range refs {
							rm, ok := v.(map[string]any)
							if !ok {
								continue
							}
							ref := parseReference(rm)
							if ref.TargetLink != "" || ref.Title != "" {
								references[k] = ref
							}
						}
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
							if o, ok := m["offense"].(string); ok && o != "" && o != "Unknown" && o != "None" {
								offense = o
							}
							if ss, ok := m["scores"].([]any); ok {
								for _, sraw := range ss {
									if sm, ok := sraw.(map[string]any); ok {
										comp, _ := sm["component"].(string)
										sc, _ := sm["score"].(float64)
										if comp != "" {
											scores = append(scores, Score{Component: comp, Score: sc})
										}
									}
								}
							}
							if st, ok := m["spokenText"].(string); ok {
								spokenText = st
							}
							if srs, ok := m["suggestedResponses"].([]any); ok && len(srs) > 0 {
								for _, srRaw := range srs {
									sr, ok := srRaw.(map[string]any)
									if !ok {
										continue
									}
									suggestions = append(suggestions, parseSuggestedResponse(sr))
								}
							}
							if mrefs, ok := m["references"].(map[string]any); ok && len(mrefs) > 0 {
								for k, v := range mrefs {
									rm, ok := v.(map[string]any)
									if !ok {
										continue
									}
									ref := parseReference(rm)
									if ref.TargetLink != "" || ref.Title != "" {
										references[k] = ref
									}
								}
							}
							if author == "bot" && mt == "" && text != "" {
								// ChatHub often sends the first visible text as a full snapshot,
								// followed by cursor deltas. Emit only the unseen suffix.
								if err := emitSnapshot(text); err != nil {
									returnConn = false
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
					if smid, ok := item["storageMessageId"].(string); ok && smid != "" {
						storageMessageID = smid
					}
					if thr, ok := item["throttling"]; ok {
						throttling = thr
					}
					if ctt, ok := item["conversationTransferToken"].(string); ok && ctt != "" {
						conversationTransferToken = ctt
					}
					if sugg, ok := item["suggestedResponses"].([]any); ok && len(sugg) > 0 && len(suggestions) == 0 {
						for _, s := range sugg {
							if sm, ok := s.(map[string]any); ok {
								sr := parseSuggestedResponse(sm)
								if sr.CommandText != "" || sr.Text != "" {
									suggestions = append(suggestions, sr)
								}
							}
						}
					}
					if res, ok := item["result"].(map[string]any); ok {
						rawResult, _ = res["value"].(string)
						if imageLimitDetected(rawResult) {
							returnConn = false
							return Result{}, ErrImageLimit
						}
						if rateLimited(rawResult) {
							returnConn = false
							return Result{}, ErrRateLimitNotice
						}
						if contentPolicyDetected(rawResult) {
							returnConn = false
							return Result{}, ErrOffensiveContent
						}
						if mi, ok := res["meteringInformation"]; ok && mi != nil {
							meteringInformation = mi
						}
						if msg, ok := res["message"].(string); ok {
							final = msg
							if imageLimitDetected(final) {
								returnConn = false
								return Result{}, ErrImageLimit
							}
							if rateLimited(final) {
								returnConn = false
								return Result{}, ErrRateLimitNotice
							}
							if IsContentPolicyBlock(final) {
								returnConn = false
								return Result{}, ErrOffensiveContent
							}
						}
					}
				}
				// completion frame often follows; keep reading a bit but we already have content
				continue
			}

			if int(t) == 3 {
				if errObj, ok := obj["error"].(map[string]any); ok {
					returnConn = false
					return Result{}, fmt.Errorf("chathub completion error: %v", errObj)
				}
				phase = PhaseCompleted
				ts.LastTokenReceived = time.Now().UTC().Format(time.RFC3339Nano)
				log.Printf("chathub timing completion_frame_ms=%d streamed_text=%d events=%d skipped_snapshots=%d", time.Since(payloadSentAt).Milliseconds(), streamed.Len(), len(events), skippedSnapshots)
				// Guard against streaming a rate-limit notice out as content
				// before finalizeText delivers a missing tail. The type-2
				// handler already rejects notice finals, so this only fires
				// on frame-order anomalies.
				if rateLimited(final) {
					returnConn = false
					return Result{}, ErrRateLimitNotice
				}
				text, ferr := finalizeText(streamed.String(), final, skippedSnapshots, emitDelta)
				if ferr != nil {
					returnConn = false
					return Result{}, ferr
				}
				if text == "" {
					text = strings.Join(deltas, "")
				}
				if imageLimitDetected(text) {
					returnConn = false
					return Result{}, ErrImageLimit
				}
				if rateLimited(text) {
					returnConn = false
					return Result{}, ErrRateLimitNotice
				}
				if text == "" {
					returnConn = false
					return Result{}, ErrEmptyCompletion
				}
				if offense != "" {
					returnConn = false
					return Result{}, ErrOffensiveContent
				}
				if IsContentPolicyBlock(text) {
					returnConn = false
					return Result{}, ErrOffensiveContent
				}
				// Healthy completion: hand the fresh-request connection back to
				// the pool instead of closing it. The deferred closer respects
				// poolEligible so continuation dials are never pooled.
				if poolEligible {
					returnConn = true
				}
				result := Result{
					Text:                      text,
					Reasoning:                 reasoningBuf.String(),
					ConversationID:            req.ConversationID,
					SessionID:                 req.SessionID,
					RequestID:                 requestID,
					Throttling:                throttling,
					SuggestedResponses:        suggestions,
					Offense:                   offense,
					Scores:                    scores,
					ConversationTransferToken: conversationTransferToken,
					MeteringInformation:       meteringInformation,
					SpokenText:                spokenText,
					StorageMessageID:          storageMessageID,
					References:                references,
					RawResult:                 rawResult,
					Events:                    events,
					Normalized:                NormalizeEvents(events),
					Images:                    imageURLs(events),
					Timestamps:                ts,
				}
				if c.Pool != nil {
					go func() {
						warmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						warmReqID := uuid.NewString()
						warmSID := uuid.NewString()
						warmCID := uuid.NewString()
						warmURL, _ := BuildWSURL(acc, warmSID, warmCID, warmReqID, req.LicenseType, req.Scenario)
						c.Pool.Warm(warmCtx, acc, warmURL)
					}()
				}
				return result, nil
			}
		}
	}

}

// finalizeText reconciles the incrementally streamed text with the
// authoritative final message carried by the SignalR type-2 result frame.
//
// emitSnapshot drops non-prefix rewrites to stay UTF-8 safe, which can leave
// the streamed buffer incomplete — or, when an early fragment poisoned the
// prefix check, diverged from the real answer (issue #51). The final message
// is upstream's source of truth, so:
//
//   - final no longer than streamed → keep the streamed text;
//   - streamed is a proper prefix of final → the stream missed the tail;
//     emit the missing part so streaming clients also receive the complete
//     answer, then return final;
//   - otherwise → the streamed prefix diverged; already-sent deltas cannot
//     be retracted, but final is returned as the Result text so non-stream
//     callers and conversation history stay correct.
func finalizeText(streamedText, final string, skipped int, emit func(string) error) (string, error) {
	if final == "" || len(final) <= len(streamedText) {
		if streamedText == "" {
			return final, nil
		}
		return streamedText, nil
	}
	if strings.HasPrefix(final, streamedText) {
		if err := emit(final[len(streamedText):]); err != nil {
			return "", err
		}
		return final, nil
	}
	log.Printf("[emitSnapshot] streamed text diverged from final result (streamed=%d final=%d skipped_snapshots=%d); using final", len(streamedText), len(final), skipped)
	return final, nil
}

func BuildWSURL(acc Account, sessionID, conversationID, requestID, licenseType, scenario string) (string, error) {
	return BuildWSURLWithOptions(acc, sessionID, conversationID, requestID, licenseType, scenario, false)
}

func BuildWSURLWithOptions(acc Account, sessionID, conversationID, requestID, licenseType, scenario string, disableMemory bool) (string, error) {
	q := url.Values{}
	q.Set("chatsessionid", requestID)
	q.Set("clientrequestid", requestID)
	q.Set("XRoutingParameterSessionKey", requestID)
	q.Set("X-SessionId", sessionID)
	q.Set("ConversationId", conversationID)
	q.Set("access_token", acc.AccessToken)
	q.Set("variants", variants)
	// source must keep quotes like the browser probe
	q.Set("source", `"officeweb"`)
	q.Set("product", "Office")
	q.Set("agentHost", "Bizchat.FullScreen")
	if licenseType != "" {
		q.Set("licenseType", licenseType)
	} else {
		q.Set("licenseType", "Starter")
	}
	q.Set("agent", "web")
	if scenario != "" {
		q.Set("scenario", scenario)
	} else {
		q.Set("scenario", "OfficeWebIncludedCopilot")
	}
	if disableMemory {
		q.Set("disableMemory", "1")
	}
	q.Set("isEdu", "false")

	// url.Values encodes quotes; probe used safe='",' so keep quotes unescaped-ish.
	// Gorilla/url will encode " to %22 which MS accepts.
	u := fmt.Sprintf("%s/%s@%s?%s", wsBase, acc.OID, acc.TID, q.Encode())
	return u, nil
}

// downloadClient returns an HTTP client used only for fetching remote image
// attachments. It reuses the shared transport (so proxy settings apply) but
// installs a redirect guard: validateRemoteDownloadURL only inspects the
// initial URL, so without re-validation a server could 302 an approved public
// URL to http://169.254.169.254/ or an internal host and defeat the SSRF
// check. Here every redirect target is re-validated and hops are capped.
func (c *Client) downloadClient() *http.Client {
	base := c.HTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	return &http.Client{
		Transport: base.Transport,
		Timeout:   base.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("attachment download: too many redirects")
			}
			return validateRemoteDownloadURL(req.URL.String())
		},
	}
}

func (c *Client) uploadAttachments(ctx context.Context, acc Account, conversationID string, attachments []Attachment) error {
	imageCount := 0
	for i := range attachments {
		a := &attachments[i]
		if a.Type != "image" {
			continue
		}
		imageCount++
		if imageCount > maxAttachments {
			return fmt.Errorf("too many image attachments: limit is %d", maxAttachments)
		}
		// For non-data URLs, download the image first
		imageData := a.URL
		if !strings.HasPrefix(a.URL, "data:") {
			if err := validateRemoteDownloadURL(a.URL); err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
			if err != nil {
				return fmt.Errorf("attachment %d: create request: %w", i, err)
			}
			resp, err := c.downloadClient().Do(req)
			if err != nil {
				return fmt.Errorf("attachment %d: download: %w", i, err)
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentMiB<<20))
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("attachment %d: read body: %w", i, err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("attachment %d: HTTP %d", i, resp.StatusCode)
			}
			mimeType := resp.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = "image/png"
			}
			imageData = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(body)
		}
		comma := strings.IndexByte(imageData, ',')
		if comma < 0 {
			return fmt.Errorf("invalid image data URL")
		}
		encoded := imageData[comma+1:]
		if !strings.Contains(strings.ToLower(imageData[:comma]), ";base64") {
			return fmt.Errorf("image URL is not base64")
		}
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return fmt.Errorf("decode image: %w", err)
		}
		form := url.Values{}
		form.Set("scenario", "UploadImage")
		form.Set("conversationId", conversationID)
		// The browser sends the complete data URL in FileBase64, including the
		// media-type prefix. UploadFile accepts this form and returns docId.
		// Live-verified 2026-08-08: UploadFile rejects multipart bodies
		// (HTTP 400 InvalidRequest); it requires x-www-form-urlencoded like
		// PyRIT's httpx client sends.
		form.Set("FileBase64", imageData)
		if c.Trace != nil {
			c.Trace(map[string]any{"stage": "upload_start", "index": i, "conversation_id": conversationID, "mime_type": a.MimeType, "base64_length": len(encoded), "token_present": acc.AccessToken != ""})
		}
		form.Add("optionsSets", "cwcgptvsan")
		form.Add("optionsSets", "flux_v3_gptv_enable_upload_multi_image_in_turn_wo_ch")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://substrate.office.com/m365Copilot/UploadFile", strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if acc.AccessToken != "" {
			req.Header.Set("Authorization", "Bearer "+acc.AccessToken)
		}
		req.Header.Set("Accept", "application/json")
		// Required by the enterprise Copilot UploadFile image-input path.
		// This feature gate is documented in the prior reverse-proxy research
		// and mirrors the PyRIT request flow.
		req.Header.Set("X-Variants", "feature.EnableImageSupportInUploadFile")
		req.Header.Set("X-Scenario", "OfficeWebIncludedCopilot")
		req.Header.Set("Referer", "https://m365.cloud.microsoft/")
		for k, vv := range c.HTTPHeader {
			for _, v := range vv {
				if k != "Origin" || v != "" {
					req.Header.Add(k, v)
				}
			}
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			log.Printf("[upload] http error: %v", err)
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			log.Printf("[upload] read error: %v", readErr)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("[upload] status %s: %s", resp.Status, strings.TrimSpace(string(data[:minInt(len(data), 500)])))
			continue
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
			log.Printf("[upload] json error: %v", err)
			continue
		}
		if out.Result.Value != "Success" || out.DocID == "" {
			log.Printf("[upload] failed: %s", strings.TrimSpace(string(data)))
			continue
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

func chatPayload(req Request, requestID string, firstTurn bool) string {
	_ = firstTurn
	locale := req.Locale
	if locale == "" {
		locale = "en-us"
	}
	tz := req.TimeZone
	if tz == "" {
		tz = "UTC"
	}
	tzOffset := req.TimeZoneOffset
	deviceOS := req.DeviceOS
	if deviceOS == "" {
		deviceOS = "Windows"
	}
	text := toolProtocolPrompt(req.Text, req.Tools, req.ToolChoice, len(clientPlugins(req.Tools, req.MCPServerURL, req.DisableWebSearch)) > 0)
	federatedConns := req.ConnectedFederatedIDs
	if len(federatedConns) == 0 {
		federatedConns = []string{"dummyId"}
	}
	fcAny := make([]any, len(federatedConns))
	for i, id := range federatedConns {
		fcAny[i] = id
	}
	clientInfo := map[string]any{
		"clientPlatform":        "mcmcopilot-web",
		"clientAppName":         "Office",
		"clientEntrypoint":      "mcmcopilot-officeweb",
		"clientSessionId":       req.SessionID,
		"ProductCategory":       "Chat",
		"clientAppType":         "Web",
		"productEntryPoint":     "ChatPanel",
		"deviceOS":              deviceOS,
		"deviceType":            "Desktop",
		"clientPlatformVersion": "10",
	}
	message := map[string]any{
		"author":                "user",
		"attachments":           req.Attachments,
		"inputMethod":           "Keyboard",
		"text":                  text,
		"entityAnnotationTypes": []string{"People", "File", "Event", "Email", "TeamsMessage"},
		"requestId":             requestID,
		"locationInfo": map[string]any{
			"timeZoneOffset": tzOffset,
			"timeZone":       tz,
		},
		"locale":                        locale,
		"messageType":                   "Chat",
		"experienceType":                "Default",
		"adaptiveCards":                 []any{},
		"clientPreferences":             map[string]any{},
		"connectedFederatedConnections": fcAny,
		"clientInfo":                    clientInfo,
	}
	// The browser does not send an OpenAI attachments array to ChatHub. It
	// sends a file annotation after the file has been uploaded by Office.
	annotations := make([]any, 0, len(req.Attachments))
	for _, a := range req.Attachments {
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
		message["connectedFederatedConnections"] = fcAny
	}
	// Restore the old gateway's multimodal injection path. The historical
	// implementation merged imageUrl/imageBase64 directly into message rather
	// than relying solely on the newer attachments array.
	for _, a := range req.Attachments {
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
		"code_interpreter_interactive_charts",
		"cwc_code_interpreter_interactive_charts_inline_image",
		"code_interpreter_matplotlib_patching",
		"cwc_fileupload_odb",
	}
	if req.FeatureFlags.MemoryV2 {
		optionsSets = append(optionsSets, "update_memory_plugin", "add_custom_instructions")
	}
	optionsSets = append(optionsSets,
		"cwc_flux_v3",
		"flux_v3_progress_messages",
		"enable_batch_token_processing",
		"enable_gg_gpt",
		"flux_v3_references",
		"flux_v3_references_entities",
		"flux_v3_references_ci",
		"add_filestore_filetype",
		"cwc_code_interpreter_citation_sourceannotations",
		"cdxcwc_code_interpreter_hallucinated_url_filter",
		"flux_v3_image_gen_enable_dimensions",
		"flux_v3_image_gen_enable_non_watermarked_storage",
		"flux_v3_image_gen_enable_icon_dimensions",
		"flux_v3_image_gen_enable_system_text_with_params",
		"flux_v3_image_gen_enable_designer_dimensions_meta_prompting_in_system_prompts",
		"flux_v3_image_gen_enable_story",
		"rich_responses")
	if req.FeatureFlags.DeepWork {
		optionsSets = append(optionsSets, "enable_deep_work")
	}
	if req.FeatureFlags.ComputerUse {
		optionsSets = append(optionsSets, "enable_computer_use")
	}
	if req.FeatureFlags.RealtimeVoice {
		optionsSets = append(optionsSets, "enable_realtime_voice")
	}
	if req.FeatureFlags.SystemPromptOverride {
		optionsSets = append(optionsSets, "enable_system_prompt_override")
	}
	if req.FeatureFlags.DesignerImageGen4o {
		optionsSets = append(optionsSets, "enable_designer_image_gen_4o")
	}
	if req.FeatureFlags.CodeCanvas {
		optionsSets = append(optionsSets, "feature.enableCodeCanvas")
	}
	if req.FeatureFlags.SydneyReconnect {
		optionsSets = append(optionsSets, "enable_sydney_reconnect")
	}
	chat := map[string]any{
		"arguments": []any{
			map[string]any{
				"source":              "officeweb",
				"clientCorrelationId": requestID,
				"sessionId":           req.SessionID,
				"optionsSets":         optionsSets,
				"options":             map[string]any{},
				"allowedMessageTypes": []string{
					"Chat", "Suggestion", "InternalSearchQuery", "Disengaged",
					"InternalLoaderMessage", "Progress", "GeneratedCode",
					"RenderCardRequest", "AdsQuery", "SemanticSerp",
					"GenerateContentQuery", "GenerateGraphicArt", "SearchQuery",
					"ConfirmationCard", "AuthError", "DeveloperLogs",
					"TriggerPlugin", "HintInvocation", "MemoryUpdate",
					"EndOfRequest", "TriggerConfirmation", "ResumeInvokeAction",
					"ResumeUserInputRequest", "TriggerUserInputRequest",
					"EscapeHatch", "TriggerPluginAuth", "ResumePluginAuth",
					"SideBySide", "ReferencesListComplete", "SwitchRespondingEndpoint",
				},
				"sliceIds":         []any{},
				"threadLevelGptId": map[string]any{},
				// HAR evidence (report 01 F12): all 12 captured sessions send
				// isStartOfSession=false even on the first turn; the WS URL
				// already binds session/conversation identity.
				"isStartOfSession": false,
				"traceId":          requestID,
				"clientInfo":       clientInfo,
				"tone":             req.Tone,
				"streamingMode":    "ConciseWithPadding",
				"message":          message,

				"plugins":                   clientPlugins(req.Tools, req.MCPServerURL, req.DisableWebSearch),
				"extraExtensionParameters":  map[string]any{},
				"isSbsSupported":            true,
				"renderReferencesBehindEOS": true,
				"disconnectBehavior":        "continue",
			},
		},
		"invocationId": "0",
		"target":       "chat",
		"type":         4,
	}
	if req.ConversationSignature != "" {
		chat["arguments"].([]any)[0].(map[string]any)["conversationSignature"] = req.ConversationSignature
	}
	if len(req.PreviousMessages) > 0 {
		chat["arguments"].([]any)[0].(map[string]any)["previousMessages"] = req.PreviousMessages
	}
	now := time.Now().UTC()
	rfc3339 := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	connStart := rfc3339(-2 * time.Second)
	userInputStart := rfc3339(-2 * time.Second)
	connEstab := rfc3339(-500 * time.Millisecond)
	userInputSubmit := rfc3339(0)
	metrics := map[string]any{
		"arguments": []any{
			map[string]any{
				"Timestamps": map[string]string{
					"ConnectionStart":       connStart,
					"UserInputStart":        userInputStart,
					"ConnectionEstablished": connEstab,
					"UserInputSubmit":       userInputSubmit,
					"RequestSent":           rfc3339(time.Millisecond),
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

// parseReference 解析单条引用。HAR 报告 05 §4.1 证实标题/摘要/来源实际
// 藏在 displayData.content 这个转义 JSON 字符串里（键名大小写混用），
// 仅读顶层字段会得到空 Title/Snippet。
func parseReference(rm map[string]any) Reference {
	ref := Reference{}
	if tl, ok := rm["targetLink"].(string); ok {
		ref.TargetLink = tl
	}
	if pdn, ok := rm["providerDisplayName"].(string); ok {
		ref.ProviderDisplayName = pdn
	}
	if t, ok := rm["title"].(string); ok {
		ref.Title = t
	}
	if s, ok := rm["snippet"].(string); ok {
		ref.Snippet = s
	}
	if lud, ok := rm["lastUpdatedDate"].(string); ok {
		ref.LastUpdatedDate = lud
	}
	if dd, ok := rm["displayData"].(map[string]any); ok {
		if content, ok := dd["content"].(string); ok && content != "" {
			var inner struct {
				Title               string `json:"Title"`
				Snippet             string `json:"snippet"`
				ProviderDisplayName string `json:"providerDisplayName"`
				LastUpdatedDate     string `json:"lastUpdatedDate"`
			}
			if json.Unmarshal([]byte(content), &inner) == nil {
				if ref.Title == "" {
					ref.Title = inner.Title
				}
				if ref.Snippet == "" {
					ref.Snippet = inner.Snippet
				}
				if ref.ProviderDisplayName == "" {
					ref.ProviderDisplayName = inner.ProviderDisplayName
				}
				if ref.LastUpdatedDate == "" {
					ref.LastUpdatedDate = inner.LastUpdatedDate
				}
			}
		}
	}
	return ref
}

func parseSuggestedResponse(m map[string]any) SuggestedResponse {
	ct, _ := m["commandText"].(string)
	t, _ := m["text"].(string)
	sc, _ := m["suggestionCategory"].(string)
	co, _ := m["contentOrigin"].(string)
	ht, _ := m["hiddenText"].(string)
	mid, _ := m["messageId"].(string)
	author, _ := m["author"].(string)
	ca, _ := m["createdAt"].(string)
	mt, _ := m["messageType"].(string)
	off, _ := m["offense"].(string)
	return SuggestedResponse{
		CommandText: ct, Text: t, SuggestionCategory: sc, ContentOrigin: co,
		HiddenText: ht, MessageID: mid, Author: author, CreatedAt: ca,
		MessageType: mt, Offense: off,
	}
}

var (
	citeOpen  = string([]rune{0xE200}) + "cite" + string([]rune{0xE202})
	citeClose = string([]rune{0xE201})
)

func StripCitationMarkers(text string, refs map[string]Reference) (string, []string) {
	if !strings.Contains(text, citeOpen) {
		return text, nil
	}
	var urls []string
	var b strings.Builder
	for {
		i := strings.Index(text, citeOpen)
		if i < 0 {
			b.WriteString(text)
			break
		}
		b.WriteString(text[:i])
		after := text[i+len(citeOpen):]
		j := strings.Index(after, citeClose)
		if j < 0 {
			b.WriteString(text[i:])
			break
		}
		refID := after[:j]
		text = after[j+len(citeClose):]
		if ref, ok := refs[refID]; ok && ref.TargetLink != "" {
			urls = append(urls, ref.TargetLink)
		}
	}
	return b.String(), urls
}
