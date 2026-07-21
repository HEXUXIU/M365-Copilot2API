package chathub

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

func imageURLs(raw []json.RawMessage) []string {
	seen := map[string]bool{}
	out := []string{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				lk := strings.ToLower(k)
				if s, ok := e.(string); ok && (lk == "url" || lk == "imageurl" || lk == "thumbnailurl" || lk == "downloadurl" || lk == "src" || lk == "value" || lk == "data") {
					if isImageURL(s) && !seen[s] {
						seen[s] = true
						out = append(out, s)
					}
				} else {
					walk(e)
				}
			}
		}
	}
	for _, r := range raw {
		var v any
		if json.Unmarshal(r, &v) == nil {
			walk(v)
		}
	}
	return out
}

func isImageURL(s string) bool {
	if strings.HasPrefix(s, "data:image/") {
		_, err := base64.StdEncoding.DecodeString(strings.SplitN(s, ",", 2)[1])
		return err == nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.Contains(p, "image") || strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".jpeg") || strings.HasSuffix(p, ".webp") || strings.HasSuffix(p, ".gif")
}
