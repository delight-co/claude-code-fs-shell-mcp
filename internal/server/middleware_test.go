package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestLog_LogsBasicMetadata(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	body := "hello world"
	handler := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(body))
	}), logger)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader("ping"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status passthrough broken: got %d, want 202", rec.Code)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log entry not valid JSON: %v\n%s", err, buf.String())
	}

	if entry["msg"] != "mcp request" {
		t.Errorf("msg = %v, want %q", entry["msg"], "mcp request")
	}
	if entry["method"] != "POST" {
		t.Errorf("method = %v, want POST", entry["method"])
	}
	if entry["path"] != "/mcp" {
		t.Errorf("path = %v, want /mcp", entry["path"])
	}
	if got := entry["status"]; got != float64(http.StatusAccepted) {
		t.Errorf("status = %v, want %d", got, http.StatusAccepted)
	}
	if got := entry["bytes"]; got != float64(len(body)) {
		t.Errorf("bytes = %v, want %d", got, len(body))
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}
}

func TestWithRequestLog_DefaultStatus200(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), logger)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log entry not valid JSON: %v\n%s", err, buf.String())
	}
	if got := entry["status"]; got != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d (default)", got, http.StatusOK)
	}
}

func TestWithRequestLog_DoesNotLogBodies(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	const (
		secretReq  = "REQUEST_BODY_SECRET_MARKER"
		secretResp = "RESPONSE_BODY_SECRET_MARKER"
	)

	handler := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(secretResp))
	}), logger)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(secretReq))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	logged := buf.String()
	if strings.Contains(logged, secretReq) {
		t.Errorf("log entry contains request body marker: %s", logged)
	}
	if strings.Contains(logged, secretResp) {
		t.Errorf("log entry contains response body marker: %s", logged)
	}
}

func TestWithRequestLog_LogsMcpSessionIDHeader(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	const wantSessionID = "test-session-abc-123"

	handler := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), logger)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader("ping"))
	req.Header.Set("Mcp-Session-Id", wantSessionID)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log entry not valid JSON: %v\n%s", err, buf.String())
	}
	if entry["mcp_session_id"] != wantSessionID {
		t.Errorf("mcp_session_id = %v, want %q", entry["mcp_session_id"], wantSessionID)
	}
}

func TestWithRequestLog_EmptyMcpSessionIDWhenHeaderAbsent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), logger)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader("ping"))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log entry not valid JSON: %v\n%s", err, buf.String())
	}
	if got := entry["mcp_session_id"]; got != "" {
		t.Errorf("mcp_session_id = %v, want empty when header is absent", got)
	}
}
