package chathub

import (
	"encoding/json"
	"testing"
)

func TestImageURLs(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"content":{"image":{"downloadUrl":"https://cdn.example.com/image/1.png","thumbnailUrl":"https://cdn.example.com/image/1.png"}},"url":"https://example.com/page"}`), json.RawMessage(`{"src":"https://cdn.example.com/image/2.webp"}`)}
	got := imageURLs(raw)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestImageURLsRejectsUnsafe(t *testing.T) {
	raw := []json.RawMessage{json.RawMessage(`{"url":"http://example.com/a.png"}`)}
	if got := imageURLs(raw); len(got) != 0 {
		t.Fatal(got)
	}
}
