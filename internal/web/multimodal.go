package web

import (
	"fmt"
	"strings"

	"m365-native/internal/chathub"
)

func parseContent(c any) (string, []chathub.Attachment) {
	var text strings.Builder
	var files []chathub.Attachment
	if s, ok := c.(string); ok {
		return s, nil
	}
	parts, ok := c.([]any)
	if !ok {
		return fmt.Sprint(c), nil
	}
	for _, raw := range parts {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "text":
			if v, ok := m["text"].(string); ok {
				text.WriteString(v)
			}
		case "image_url":
			if u, ok := m["image_url"].(map[string]any); ok {
				if v, ok := u["url"].(string); ok {
					files = append(files, chathub.Attachment{Type: "image", URL: v, MimeType: "image/*"})
				}
			}
		case "image":
			if u, ok := m["url"].(string); ok {
				files = append(files, chathub.Attachment{Type: "image", URL: u, MimeType: "image/*"})
			}
		case "file":
			f := chathub.Attachment{Type: "file"}
			if v, ok := m["file_id"].(string); ok {
				f.URL = v
			}
			if v, ok := m["filename"].(string); ok {
				f.Name = v
			}
			if v, ok := m["mime_type"].(string); ok {
				f.MimeType = v
			}
			files = append(files, f)
		}
	}
	return text.String(), files
}
