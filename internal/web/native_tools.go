package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"m365-native/internal/chathub"
)

// nativeToolCalls converts only tool invocations actually present in ChatHub
// frames. It never infers a call from the user's text and never makes a second request.
func nativeToolCalls(events []json.RawMessage, tools []chathub.Tool) []detectedToolCall {
	allowed := map[string]bool{}
	for _, t := range tools {
		var f struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(t.Function, &f) == nil && f.Name != "" {
			allowed[f.Name] = true
		}
	}
	var out []detectedToolCall
	for _, raw := range events {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walkNative(v, allowed, &out)
		}
	}
	return out
}
func walkNative(v any, allowed map[string]bool, out *[]detectedToolCall) {
	switch x := v.(type) {
	case []any:
		for _, y := range x {
			walkNative(y, allowed, out)
		}
	case map[string]any:
		name := ""
		for _, k := range []string{"name", "toolName", "pluginName", "functionName", "id"} {
			if s, ok := x[k].(string); ok && allowed[s] {
				name = s
				break
			}
		}
		if name != "" {
			var a any
			for _, k := range []string{"arguments", "args", "parameters", "input", "functionArguments"} {
				if z, ok := x[k]; ok {
					a = z
					break
				}
			}
			if a != nil {
				b, _ := json.Marshal(a)
				h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s", name, b)))
				*out = append(*out, detectedToolCall{ID: "call_" + hex.EncodeToString(h[:8]), Name: name, Arguments: b})
				return
			}
		}
		for _, y := range x {
			walkNative(y, allowed, out)
		}
	}
}
