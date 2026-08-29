package web

import (
	"fmt"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestLimitPromptImageAttachmentsPrefersExplicitAndRecent(t *testing.T) {
	attachments := []chathub.Attachment{
		{Type: "image", Name: "explicit-1"},
		{Type: "image", Name: "explicit-2"},
		{Type: "file", Name: "document"},
	}
	for i := 0; i < 12; i++ {
		attachments = append(attachments, chathub.Attachment{Type: "image", Name: fmt.Sprintf("history-%02d", i)})
	}
	got := limitPromptImageAttachments(attachments, 2)
	if len(got) != 11 {
		t.Fatalf("attachments=%d want 11 (10 images + file): %#v", len(got), got)
	}
	names := map[string]bool{}
	for _, attachment := range got {
		names[attachment.Name] = true
	}
	for _, required := range []string{"explicit-1", "explicit-2", "document", "history-04", "history-11"} {
		if !names[required] {
			t.Fatalf("required attachment %q was dropped: %#v", required, got)
		}
	}
	for _, dropped := range []string{"history-00", "history-01", "history-02", "history-03"} {
		if names[dropped] {
			t.Fatalf("old attachment %q was retained: %#v", dropped, got)
		}
	}
}
