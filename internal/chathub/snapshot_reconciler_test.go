package chathub

import (
	"strings"
	"testing"
)

func TestSnapshotReconcilerAppendsPrefixGrowth(t *testing.T) {
	var r snapshotReconciler
	s1, ok1 := r.Apply("Hello")
	if !ok1 || s1 != "Hello" {
		t.Fatalf("first=%q ok=%t", s1, ok1)
	}
	s2, ok2 := r.Apply("Hello, world")
	if !ok2 || s2 != ", world" {
		t.Fatalf("suffix=%q ok=%t", s2, ok2)
	}
	s3, ok3 := r.Apply("Hello, world!")
	if !ok3 || s3 != "!" {
		t.Fatalf("suffix=%q ok=%t", s3, ok3)
	}
}

func TestSnapshotReconcilerIgnoresShorterAndNonPrefix(t *testing.T) {
	var r snapshotReconciler
	r.Apply("long snapshot text")
	if s, ok := r.Apply("short"); ok || s != "" {
		t.Fatalf("shorter accepted: %q ok=%t", s, ok)
	}
	if s, ok := r.Apply("different branch"); ok || s != "" {
		t.Fatalf("non-prefix accepted: %q ok=%t", s, ok)
	}
	if s, ok := r.Apply("long snapshot text!"); !ok || s != "!" {
		t.Fatalf("recovery failed: %q ok=%t", s, ok)
	}
}

func TestSnapshotReconcilerRejectsEmptyAndDuplicate(t *testing.T) {
	var r snapshotReconciler
	if s, ok := r.Apply(""); ok || s != "" {
		t.Fatalf("empty accepted: %q ok=%t", s, ok)
	}
	r.Apply("abc")
	if s, ok := r.Apply("abc"); ok || s != "" {
		t.Fatalf("duplicate accepted: %q ok=%t", s, ok)
	}
}

func TestSnapshotReconcilerMultibyteSafety(t *testing.T) {
	var r snapshotReconciler
	s1, ok1 := r.Apply("中文测试")
	if !ok1 || s1 != "中文测试" {
		t.Fatalf("first=%q", s1)
	}
	s2, ok2 := r.Apply("中文测试二")
	if !ok2 || s2 != "二" {
		t.Fatalf("multibyte suffix=%q", s2)
	}
	// A snapshot truncated mid-rune must not emit a partial suffix.
	trimmed := "中文测试" + "二"[:1]
	if s, ok := r.Apply(trimmed); ok || s != "" {
		t.Fatalf("mid-rune accepted: %q ok=%t", s, ok)
	}
}

func TestSnapshotReconcilerNeverSplitsRunes(t *testing.T) {
	var r snapshotReconciler
	r.Apply("héllo")
	suffix, ok := r.Apply("héllo wörld")
	if !ok {
		t.Fatal("valid growth rejected")
	}
	if !strings.Contains(suffix, "wörld") {
		t.Fatalf("suffix=%q", suffix)
	}
}
