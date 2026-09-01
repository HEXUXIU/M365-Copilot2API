package web

import (
	"strings"
	"unicode"
)

func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// toolProgressText is deliberately short and action-oriented. It gives the
// caller an immediate human status without exposing router control JSON or
// upstream chain-of-thought content.
func toolProgressText(prompt string, calls []detectedToolCall) string {
	zh := containsHan(prompt)
	name := ""
	if len(calls) > 0 {
		name = strings.ToLower(strings.TrimSpace(calls[0].Name))
	}
	if zh {
		switch name {
		case "exec":
			return "我先检查当前环境并执行这一步。"
		case "apply_patch":
			return "我先核对目标文件，再应用修改。"
		case "view_image":
			return "我先读取图片内容，确认关键细节。"
		default:
			return "我先处理这一步，并核对返回结果。"
		}
	}
	switch name {
	case "exec":
		return "I’ll check the current environment and carry this out."
	case "apply_patch":
		return "I’ll verify the target files, then apply the change."
	case "view_image":
		return "I’ll read the image and verify the important details."
	default:
		return "I’ll handle this step and verify the result."
	}
}

func toolProgressContinuation(prompt string) string {
	if containsHan(prompt) {
		return "处理路径已经确定，我继续执行并核对结果。"
	}
	return "The next step is clear; I’ll run it and verify the result."
}
