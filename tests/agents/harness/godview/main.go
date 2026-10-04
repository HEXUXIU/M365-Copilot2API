// Command godview is a logging reverse proxy placed in front of the gateway.
// It forwards every request untouched, but records the full request and
// response — including streamed SSE frames and the overlap of concurrent
// sessions — to a JSONL file and a live console summary. Agents (opencode,
// codex, ...) point their base URL at godview instead of the gateway, giving a
// complete external view of what the gateway actually sent and received.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type record struct {
	ID          int64  `json:"id"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status,omitempty"`
	Stream      bool   `json:"stream"`
	ReqBody     string `json:"req_body,omitempty"`
	RespBody    string `json:"resp_body,omitempty"`
	RespTrunc   bool   `json:"resp_truncated,omitempty"`
	Frames      int    `json:"sse_frames,omitempty"`
	FirstByteMs int64  `json:"first_byte_ms,omitempty"`
	Err         string `json:"error,omitempty"`
	Client      string `json:"client,omitempty"`
}

var (
	target  = flag.String("target", "http://127.0.0.1:4141", "gateway base URL to forward to")
	listen  = flag.String("listen", "127.0.0.1:4142", "address to listen on")
	logDir  = flag.String("logdir", "godview-logs", "directory for the JSONL transcript")
	maxBody = flag.Int("maxbody", 1<<20, "max bytes captured per direction")
	quiet   = flag.Bool("quiet", false, "suppress the live console summary")
)

func main() {
	flag.Parse()
	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		log.Fatalf("godview: create logdir: %v", err)
	}
	path := filepath.Join(*logDir, "transcript-"+time.Now().Format("20060102-150405")+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("godview: create transcript: %v", err)
	}
	defer f.Close()
	var mu sync.Mutex
	var seq int64
	write := func(r record) {
		b, _ := json.Marshal(r)
		mu.Lock()
		f.Write(append(b, '\n'))
		mu.Unlock()
		if !*quiet {
			log.Printf("#%d %s %s -> %d %dms stream=%v frames=%d first_byte=%dms",
				r.ID, r.Method, r.Path, r.Status, r.DurationMs, r.Stream, r.Frames, r.FirstByteMs)
		}
	}

	client := &http.Client{Transport: &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}}
	base, err := url.Parse(*target)
	if err != nil {
		log.Fatalf("godview: bad target %q: %v", *target, err)
	}
	h := func(w http.ResponseWriter, req *http.Request) {
		id := atomic.AddInt64(&seq, 1)
		start := time.Now()
		body, _ := io.ReadAll(req.Body)
		req.Body.Close()

		up := req.Clone(req.Context())
		up.RequestURI = ""
		up.Host = ""
		up.URL.Scheme = base.Scheme
		up.URL.Host = base.Host
		up.Body = io.NopCloser(bytes.NewReader(body))
		up.ContentLength = int64(len(body))
		up.Header = req.Header.Clone()
		up.Header.Set("X-Godview-Id", fmt.Sprint(id))

		resp, err := client.Do(up)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			write(record{ID: id, StartedAt: start.Format(time.RFC3339Nano), Method: req.Method,
				Path: req.URL.Path, Status: http.StatusBadGateway, Err: err.Error(),
				ReqBody: capBody(body, *maxBody), Client: req.Header.Get("User-Agent")})
			return
		}
		defer resp.Body.Close()

		stream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		fl, _ := w.(http.Flusher)

		var firstByte time.Duration
		var frames int
		var buf bytes.Buffer
		br := bufio.NewReaderSize(resp.Body, 32*1024)
		tmp := make([]byte, 32*1024)
		for {
			n, rerr := br.Read(tmp)
			if n > 0 {
				if firstByte == 0 {
					firstByte = time.Since(start)
				}
				if buf.Len() < *maxBody {
					buf.Write(tmp[:n])
				}
				if _, werr := w.Write(tmp[:n]); werr != nil {
					break
				}
				if fl != nil {
					fl.Flush()
				}
				if stream {
					frames += bytes.Count(tmp[:n], []byte("\n\n"))
				}
			}
			if rerr != nil {
				break
			}
		}
		write(record{
			ID: id, StartedAt: start.Format(time.RFC3339Nano), EndedAt: time.Now().Format(time.RFC3339Nano),
			DurationMs: time.Since(start).Milliseconds(), Method: req.Method, Path: req.URL.Path,
			Status: resp.StatusCode, Stream: stream, ReqBody: capBody(body, *maxBody),
			RespBody: capBody(buf.Bytes(), *maxBody), RespTrunc: buf.Len() >= *maxBody,
			Frames: frames, FirstByteMs: firstByte.Milliseconds(), Client: req.Header.Get("User-Agent"),
		})
	}

	srv := &http.Server{Addr: *listen, Handler: http.HandlerFunc(h), ReadHeaderTimeout: 15 * time.Second}
	log.Printf("godview listening on http://%s -> %s (transcript %s)", *listen, *target, path)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("godview: %v", err)
	}
}

func capBody(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + "\n...[truncated]"
	}
	return string(b)
}
