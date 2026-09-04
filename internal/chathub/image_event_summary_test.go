package chathub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeImageEventsRedactsSensitiveValues(t *testing.T) {
	raw := json.RawMessage(`{
	  "type": 1,
	  "target": "update",
	  "requestId": "c5f3901c-f8e5-4c29-93de-72f0b19e22c8",
	  "arguments": [{
	    "prompt": "private prompt",
	    "messages": [{
	      "messageType": "Progress",
	      "contentType": "image",
	      "contentOrigin": "ImageGeneration",
	      "text": "private prompt",
	      "email": "person@example.com",
	      "contentGenerationProgressList": [{
	        "contentType": "image",
	        "status": 2,
	        "pollUrl": "poll-secret",
	        "fileToken": "file-secret",
	        "ImageReferenceUrls": [
	          "https://designerapp.officeapps.live.com/designerapp/document.ashx?path=%2Ffixture%2FDallEGeneratedImages%2Ffixture.png&fileToken=secret"
	          ]
	      }]
	    }, {
	      "messageType": "private-secret",
	      "contentType": "private-content",
	      "contentOrigin": "private-origin"
	    }]
	  }]
	}`)

	got := SummarizeImageEvents([]json.RawMessage{raw})
	if len(got) != 1 {
		t.Fatalf("summary count=%d, want 1", len(got))
	}
	event := got[0]
	if !event.Parsed || event.EventType != 1 || event.Target != "update" {
		t.Fatalf("unexpected event summary: %#v", event)
	}
	if event.ImageCandidateCount != 1 {
		t.Fatalf("image candidates=%d, want 1", event.ImageCandidateCount)
	}
	if len(event.Arguments) != 1 || len(event.Arguments[0].Messages) != 2 {
		t.Fatalf("missing message structure: %#v", event.Arguments)
	}
	message := event.Arguments[0].Messages[0]
	if message.MessageType != "Progress" || message.ContentType != "image" || message.ContentOrigin != "ImageGeneration" {
		t.Fatalf("unexpected protocol enums: %#v", message)
	}
	if len(message.Progress) != 1 || message.Progress[0].Status != "2" ||
		message.Progress[0].ContentType != "image" || message.Progress[0].URLCount != 1 {
		t.Fatalf("unexpected progress summary: %#v", message.Progress)
	}
	unknown := event.Arguments[0].Messages[1]
	if unknown.MessageType != "other" || unknown.ContentType != "other" || unknown.ContentOrigin != "other" {
		t.Fatalf("unknown protocol values were not redacted: %#v", unknown)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	summary := string(encoded)
	for _, forbidden := range []string{
		"private prompt",
		"person@example.com",
		"Bearer ",
		"poll-secret",
		"file-secret",
		"c5f3901c-f8e5-4c29-93de-72f0b19e22c8",
		"https://designerapp.officeapps.live.com/",
		"private-secret",
		"private-content",
		"private-origin",
	} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, summary)
		}
	}
}

func TestSummarizeImageEventsClassifiesCompletionAndInvalidJSON(t *testing.T) {
	invalid := json.RawMessage(`{"token":"Bearer top-secret"`)
	completion := json.RawMessage(`{
	  "type": 2,
	  "item": {
	    "result": {
	      "value": "Success",
	      "message": "private completion",
	      "serviceVersion": "secret-version"
	    }
	  }
	}`)

	got := SummarizeImageEvents([]json.RawMessage{invalid, completion})
	if len(got) != 2 {
		t.Fatalf("summary count=%d, want 2", len(got))
	}
	if got[0].Parsed || got[0].Bytes != len(invalid) {
		t.Fatalf("invalid event summary: %#v", got[0])
	}
	if !got[1].Parsed || got[1].EventType != 2 || got[1].ResultClass != "success" {
		t.Fatalf("completion summary: %#v", got[1])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	summary := string(encoded)
	for _, forbidden := range []string{"top-secret", "private completion", "secret-version"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, summary)
		}
	}
}
