package web

import (
	"encoding/json"
	"os"
	"strings"

	"m365-copilot2api/internal/chathub"
)

// webSearchDisabled reports whether M365_ENABLE_WEB_SEARCH turns off the
// automatic web_search declaration. Web search is enabled by default and is
// only disabled by an explicit 0/false/off/no value.
func webSearchDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("M365_ENABLE_WEB_SEARCH"))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// ensureWebSearchEnabled injects the built-in web_search tool declaration
// when the client did not already declare it, so ChatHub registers the
// BingWebSearch built-in and can ground answers in live search results.
// Disable with M365_ENABLE_WEB_SEARCH=0 (or false/off).
func ensureWebSearchEnabled(body *oaiReq) {
	if webSearchDisabled() {
		return
	}
	for _, t := range body.Tools {
		isWebSearchType := strings.EqualFold(t.Type, "web_search")
		var f struct{ Name string }
		if json.Unmarshal(t.Function, &f) == nil && strings.EqualFold(f.Name, "web_search") {
			isWebSearchType = true
		}
		if isWebSearchType {
			return // client already declared it
		}
	}
	body.Tools = append(body.Tools, chathub.Tool{Type: "web_search"})
}
