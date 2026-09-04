package web

import (
	"encoding/json"
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestImageEventDiagnosticReportsStructureWithoutContent(t *testing.T) {
	raw := json.RawMessage(`{
	  "type": 1,
	  "target": "update",
	  "arguments": [{
	    "messages": [{
	      "messageType": "Progress",
	      "contentType": "image",
	      "contentOrigin": "ImageGeneration",
	      "text": "private image prompt",
	      "contentGenerationProgressList": [{
	        "contentType": "image",
	        "status": 1,
	        "pollUrl": "private-poll-token",
	        "fileToken": "private-file-token",
	        "ImageReferenceUrls": []
	      }]
	    }]
	  }]
	}`)
	res := chathub.Result{
		Reasoning:      "private chain-of-thought",
		TerminalReason: "response_idle_with_content",
		Events:         []json.RawMessage{raw},
	}

	got := imageEventDiagnostic(res)
	for _, want := range []string{
		`"events":1`,
		`"reasoning_bytes":24`,
		`"terminal_reason":"response_idle_with_content"`,
		`"message_type":"Progress"`,
		`"content_type":"image"`,
		`"status":"1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{
		"private image prompt",
		"private chain-of-thought",
		"private-poll-token",
		"private-file-token",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("diagnostic leaked %q: %s", forbidden, got)
		}
	}
}

func TestImageEventDiagnosticSkipsSuccessfulImageResult(t *testing.T) {
	res := chathub.Result{
		Images:         []string{"https://example.com/image.png"},
		Reasoning:      "private reasoning",
		TerminalReason: "completion_frame",
	}
	if got := imageEventDiagnostic(res); got != "" {
		t.Fatalf("successful image diagnostic=%q, want empty", got)
	}
}
