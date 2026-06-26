package server

import (
	"log/slog"
	"net/http"
	"time"
)

// withRequestLog wraps an HTTP handler so each incoming request is logged
// after it has been served. The log entry includes the request method, the
// URL path, the response status code, the elapsed time, and the number of
// response body bytes written.
//
// Request and response bodies themselves are never logged: tool arguments
// (including file paths) and file contents must not appear in operational
// logs.
func withRequestLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("mcp request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", rec.bytes,
			"mcp_session_id", r.Header.Get("Mcp-Session-Id"),
		)
	})
}

// responseRecorder wraps http.ResponseWriter to capture the status code and
// the number of bytes written to the body. It also forwards Flush calls so
// streaming responses (server-sent events) keep working.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
