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
		typ, _ := m["type"].(string)
		// Responses API uses input_text and may put image_url directly on
		// the content item rather than nesting it under image_url.
		if v, ok := m["text"].(string); ok && (typ == "text" || typ == "input_text" || typ == "output_text" || typ == "") {
			text.WriteString(v)
		}
		if direct, ok := m["image_url"].(string); ok && direct != "" {
			files = append(files, chathub.Attachment{Type: "image", URL: direct, MimeType: "image/*"})
		}
		switch typ {
		case "text", "input_text", "output_text":
			// handled above
		case "image_url":
			if u, ok := m["image_url"].(map[string]any); ok {
				if v, ok := u["url"].(string); ok {
					a := chathub.Attachment{Type: "image", URL: v, MimeType: "image/*"}
					if d, ok := u["detail"].(string); ok {
						a.Detail = d
					}
					files = append(files, a)
				}
			}
		case "input_image", "image":
			// Responses API accepts both image_url as a string and image_url
			// as an object containing url. Also accept nested source.url/data.
			u := stringValue(m, "image_url", "url", "source")
			if raw, ok := m["image_url"].(map[string]any); ok {
				u = stringValue(raw, "url", "data", "image_url")
			}
			if raw, ok := m["source"].(map[string]any); ok && u == "" {
				u = stringValue(raw, "url", "data", "source")
			}
			if u != "" {
				files = append(files, chathub.Attachment{Type: "image", URL: u, MimeType: "image/*"})
			}
		case "input_file", "file":
			u := stringValue(m, "file_data", "file_url", "url", "source", "file_id")
			if u != "" || stringValue(m, "filename", "name") != "" {
				files = append(files, chathub.Attachment{Type: "file", URL: u, Name: stringValue(m, "filename", "name"), MimeType: stringValue(m, "mime_type", "mimeType", "content_type")})
			}
		case "input_audio", "audio":
			u := stringValue(m, "data", "audio_url", "url", "source")
			if u != "" {
				files = append(files, chathub.Attachment{Type: "audio", URL: u, MimeType: stringValue(m, "mime_type", "mimeType", "format", "content_type")})
			}
		}
	}
	return text.String(), files
}

func stringValue(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
