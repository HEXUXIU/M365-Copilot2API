package web

import (
	"log"
	"net/http"
	"runtime/debug"
)

// streamingWriter 跟踪 header 是否已写出，供 recover 分支使用。
type streamingWriter struct {
	http.ResponseWriter
	headerWritten bool
}

func (w *streamingWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.headerWritten = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *streamingWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.headerWritten = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *streamingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// recoverPanics 捕获 handler panic，已开始流式时不写错误体，否则返回 JSON 500。
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &streamingWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[recover] panic on %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				if !sw.headerWritten {
					writeOpenAIError(sw, 500, "internal_error", "internal_error", "internal error")
				}
			}
		}()
		next.ServeHTTP(sw, r)
	})
}