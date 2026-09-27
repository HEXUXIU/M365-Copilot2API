package web

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseBufferPoolRejectsLargeBuffer(t *testing.T) {
	b := bytes.NewBuffer(make([]byte, 0, maxPooledBufferCapacity+1))
	putResponseBuffer(b)
	if b.Len() != 0 {
		t.Fatalf("large buffer length = %d, want 0", b.Len())
	}
}

func TestResponseBufferPoolHasBoundedCapacity(t *testing.T) {
	for {
		select {
		case <-responseBufferPool:
		default:
			goto drained
		}
	}

drained:
	for i := 0; i < maxPooledBuffers+1; i++ {
		putResponseBuffer(new(bytes.Buffer))
	}
	if len(responseBufferPool) != maxPooledBuffers {
		t.Fatalf("pooled buffer count = %d, want %d", len(responseBufferPool), maxPooledBuffers)
	}
}

func TestSSEWriterDefersCommitUntilFirstWrite(t *testing.T) {
	r := httptest.NewRecorder()
	sw := newSSEWriter(r, nil)
	if sw.isCommitted() || r.Header().Get("Content-Type") != "" || r.Body.Len() != 0 {
		t.Fatal("writer committed before first SSE byte")
	}
	if err := sw.data("hello"); err != nil {
		t.Fatal(err)
	}
	if !sw.isCommitted() || r.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatal("writer did not commit SSE response on first write")
	}
}

func TestCacheStatsEvictsOldestKey(t *testing.T) {
	s := &CacheStats{KeyStats: make(map[string]*KeyStat), persist: &persistStore{}}
	for i := 0; i < maxCacheStatKeys; i++ {
		key := string(rune(i + 1))
		s.KeyStats[key] = &KeyStat{APIKey: key, LastUsed: time.Unix(int64(i+1), 0)}
	}
	s.RecordRequest("new", true, 1, 1, 0)
	if len(s.KeyStats) != maxCacheStatKeys {
		t.Fatalf("key count = %d, want %d", len(s.KeyStats), maxCacheStatKeys)
	}
	if _, ok := s.KeyStats[string(rune(1))]; ok {
		t.Fatal("oldest key was not evicted")
	}
	if _, ok := s.KeyStats["new"]; !ok {
		t.Fatal("new key was not recorded")
	}
}
