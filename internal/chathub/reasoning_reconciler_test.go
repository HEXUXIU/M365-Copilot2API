package chathub

import "testing"

func TestReasoningReconcilerEmitsOnlyNovelSnapshotText(t *testing.T) {
	var r reasoningReconciler
	for _, tc := range []struct {
		in, want string
	}{
		{"我先检查环境。", "我先检查环境。"},
		{"我先检查环境。", ""},
		{"我先检查环境。然后读取配置。", "然后读取配置。"},
		{"我先检查环境。", ""},
	} {
		if got := r.Apply(tc.in); got != tc.want {
			t.Fatalf("Apply(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}
