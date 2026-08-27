package web

import (
	"encoding/json"
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func answerRequestTestBody() oaiReq {
	return oaiReq{
		ConversationID: "conversation-1",
		SessionID:      "session-1",
		Tools: []chathub.Tool{{
			Type:     "function",
			Function: json.RawMessage(`{"name":"read_file","parameters":{"type":"object"}}`),
		}},
		ToolChoice: "auto",
	}
}

func TestBuildAnswerRequestRouterOmitsNativePlugins(t *testing.T) {
	req := buildAnswerRequest("[user]\nhello", "magic", answerRequestTestBody(), agentLedger{}, "router", "legacy", "", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 0 || req.ToolChoice != nil {
		t.Fatalf("router answer leaked native tools: tools=%d choice=%#v", len(req.Tools), req.ToolChoice)
	}
	if req.Text != "[user]\nhello" {
		t.Fatalf("empty ledger changed answer prompt: %q", req.Text)
	}
}

func TestBuildAnswerRequestNativeForwardsTools(t *testing.T) {
	req := buildAnswerRequest("[user]\nhello", "magic", answerRequestTestBody(), agentLedger{}, "native", "legacy", "", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 1 || req.ToolChoice != "auto" {
		t.Fatalf("native answer lost tools: tools=%d choice=%#v", len(req.Tools), req.ToolChoice)
	}
}

func TestBuildAnswerRequestAddsCompletedEvidence(t *testing.T) {
	ledger := agentLedger{Completed: []toolEvidence{{ID: "call_1", Name: "read_file", Arguments: `{}`, Result: "ok"}}}
	req := buildAnswerRequest("[user]\nsummarize", "magic", answerRequestTestBody(), ledger, "router", "legacy", "", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	for _, want := range []string{"EVIDENCE_LEDGER:", "Report only actions supported by completed tool results"} {
		if !strings.Contains(req.Text, want) {
			t.Fatalf("answer prompt missing %q: %s", want, req.Text)
		}
	}
}

func TestBuildAnswerRequestMCPForwardsTools(t *testing.T) {
	req := buildAnswerRequest("[user]\nhello", "magic", answerRequestTestBody(), agentLedger{}, "router", "legacy", "http://127.0.0.1:4142/v1/mcp/sse", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 1 || req.ToolChoice != "auto" {
		t.Fatalf("MCP answer lost tools: tools=%d choice=%#v", len(req.Tools), req.ToolChoice)
	}
	if req.MCPServerURL != "http://127.0.0.1:4142/v1/mcp/sse" {
		t.Fatalf("MCP URL not set: %q", req.MCPServerURL)
	}
}

func TestBuildAnswerRequestPiCompatRouterPreservesUpstreamToolContext(t *testing.T) {
	req := buildAnswerRequest("[user]\nhello", "magic", answerRequestTestBody(), agentLedger{}, "router", "pi_compat", "http://127.0.0.1:4142/v1/mcp/sse", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 1 || req.ToolChoice != "auto" || req.MCPServerURL == "" {
		t.Fatalf("pi_compat router lost upstream tool context: tools=%d choice=%#v mcp=%q", len(req.Tools), req.ToolChoice, req.MCPServerURL)
	}
}

func TestBuildAnswerRequestPiCompatNativeKeepsUpstreamTools(t *testing.T) {
	req := buildAnswerRequest("[user]\nhello", "magic", answerRequestTestBody(), agentLedger{}, "native", "pi_compat", "http://127.0.0.1:4142/v1/mcp/sse", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 1 || req.ToolChoice != "auto" || req.MCPServerURL == "" {
		t.Fatalf("pi_compat native lost upstream planner: tools=%d choice=%#v mcp=%q", len(req.Tools), req.ToolChoice, req.MCPServerURL)
	}
}

func TestBuildAnswerRequestRouterHidesMCPAfterCompletedTool(t *testing.T) {
	ledger := agentLedger{Completed: []toolEvidence{{ID: "call_1", Name: "read_file", Arguments: `{}`, Result: "ok"}}}
	req := buildAnswerRequest("[user]\nsummarize", "magic", answerRequestTestBody(), ledger, "router", "pi_compat", "http://127.0.0.1:4142/v1/mcp/sse", runtimeSettings{}, chathub.FeatureFlags{}, chathubLocale{}, false)
	if len(req.Tools) != 1 || req.MCPServerURL != "" {
		t.Fatalf("router finalization should retain schema and hide executor: tools=%d mcp=%q", len(req.Tools), req.MCPServerURL)
	}
}
