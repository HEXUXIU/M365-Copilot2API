package web

import (
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestImageProgressStageFromEvent(t *testing.T) {
	tests := []struct {
		name  string
		event chathub.StreamEvent
		want  string
	}{
		{
			name:  "pending GraphicArt",
			event: chathub.StreamEvent{Kind: "progress", MessageType: "Progress", ContentType: "GraphicArt", ContentOrigin: "ImageGeneration"},
			want:  "generating",
		},
		{
			name:  "pending ImageGeneration",
			event: chathub.StreamEvent{Kind: "progress", MessageType: "Progress", ContentType: "ImageGeneration", ContentOrigin: "ImageGeneration"},
			want:  "generating",
		},
		{
			name:  "ordinary text",
			event: chathub.StreamEvent{Kind: "text", Text: "hello"},
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageProgressStageFromEvent(tc.event); got != tc.want {
				t.Fatalf("stage=%q, want %q", got, tc.want)
			}
		})
	}
}
