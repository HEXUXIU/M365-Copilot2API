package chathub

import "testing"

func TestSnapshotReconcilerConsumesRedundantChannelsOnce(t *testing.T) {
	var r snapshotReconciler
	var got string
	for _, snapshot := range []string{
		"已生成并验证：D:\\chat\\pelican-bicycle.svg。",
		"已生成并验证：D:\\chat\\pelican-bicycle.svg。",
		"已生成并验证：D:\\chat\\pelican-bicycle.svg。下一步检查文件。",
	} {
		if suffix, ok := r.Apply(snapshot); ok {
			got += suffix
		}
	}
	want := "已生成并验证：D:\\chat\\pelican-bicycle.svg。下一步检查文件。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSnapshotReconcilerIgnoresRewritesAndShorterFrames(t *testing.T) {
	var r snapshotReconciler
	if suffix, ok := r.Apply("前缀"); !ok || suffix != "前缀" {
		t.Fatalf("initial snapshot suffix=%q ok=%v", suffix, ok)
	}
	for _, snapshot := range []string{"前", "不同的重写", "前缀"} {
		if suffix, ok := r.Apply(snapshot); ok || suffix != "" {
			t.Fatalf("rewrite snapshot %q produced suffix=%q ok=%v", snapshot, suffix, ok)
		}
	}
	if suffix, ok := r.Apply("前缀尾部"); !ok || suffix != "尾部" {
		t.Fatalf("recovered snapshot suffix=%q ok=%v", suffix, ok)
	}
}
