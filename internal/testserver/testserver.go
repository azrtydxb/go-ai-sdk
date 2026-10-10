// Package testserver provides the small HTTP fixture-server scaffolding
// shared by the wire-format compattest packages
// (internal/openaicompat/compattest, internal/geminicompat/compattest): a
// mutex-guarded recording Server, goroutine-safe body reading and decoding,
// SSE writing, and the canned error scenarios both fixtures answer with.
//
// It deliberately knows nothing about any wire format — URL routes, request
// decoding into format-specific wire types, scenario mapping, and response
// shapes stay in the per-format compattest packages.
package testserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Server is a recording httptest.Server: it keeps every request body and
// header it receives, so tests can assert on the wire shape after the fact.
// Callers must Close it (or rely on the t.Cleanup registered by New).
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	requests [][]byte
	headers  []http.Header
}

// New starts handler on a local httptest.Server and registers Close via
// t.Cleanup.
func New(t *testing.T, handler http.Handler) *Server {
	t.Helper()
	s := &Server{}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s.Server = srv
	return s
}

// Requests returns the raw JSON bodies of every request received so far, in
// arrival order.
func (s *Server) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.requests))
	copy(out, s.requests)
	return out
}

// AuthHeaders returns the Authorization header value of every request
// received so far, in arrival order (one entry per Requests() entry).
func (s *Server) AuthHeaders() []string {
	return s.HeaderValues("Authorization")
}

// HeaderValues returns the named header's value for every request received
// so far, in arrival order (one entry per Requests() entry). Missing headers
// yield "" for that entry.
func (s *Server) HeaderValues(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.headers))
	for i, h := range s.headers {
		out[i] = h.Get(name)
	}
	return out
}

// Record appends a request body and its headers to the recording log.
func (s *Server) Record(raw []byte, header http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, raw)
	s.headers = append(s.headers, header)
}

// ReadBody reads r's body, reporting via t.Errorf and writing a 500 response
// on failure. It returns ok=false when the caller (a handler running on its
// own goroutine) should stop processing the request; t.Fatalf is not
// goroutine-safe per the testing docs, so handler code must use this pattern
// instead of failing the test directly.
func ReadBody(t *testing.T, w http.ResponseWriter, r *http.Request) (buf []byte, ok bool) {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		msg := fmt.Sprintf("testserver: read body: %v", err)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return nil, false
	}
	return buf, true
}

// DecodeAndRecord reads r's body, decodes it into req, and records the raw
// body plus the request headers, reporting via t.Errorf and writing a 500 on
// failure. It returns ok=false when the caller (a handler running on its own
// goroutine) should stop processing the request.
func DecodeAndRecord(t *testing.T, s *Server, w http.ResponseWriter, r *http.Request, req any) (ok bool) {
	t.Helper()
	raw, ok := ReadBody(t, w, r)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, req); err != nil {
		msg := fmt.Sprintf("testserver: decode request: %v", err)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return false
	}
	s.Record(raw, r.Header.Clone())
	return true
}

// WriteJSONError writes a JSON error body of the shape
// {"error":{"message":<msg>}} with the given status code — the canned error
// the fixtures answer the "fail 429"/"fail 400" scenarios with.
func WriteJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, msg)
}

// ErrorScenario handles the error scenarios keyed off the last user message
// text that both fixtures share: "fail 429" and "fail 400". It returns
// handled=true when it wrote a response and the caller must stop.
func ErrorScenario(w http.ResponseWriter, text string) (handled bool) {
	switch text {
	case "fail 429":
		WriteJSONError(w, 429, "rate limited")
		return true
	case "fail 400":
		WriteJSONError(w, 400, "bad request")
		return true
	}
	return false
}

// StartSSE switches w to a streaming response (text/event-stream, 200 OK)
// and returns the Flusher, reporting via t.Errorf and writing a 500 if the
// ResponseWriter does not support flushing. ok=false means the caller must
// stop.
func StartSSE(t *testing.T, w http.ResponseWriter) (flusher http.Flusher, ok bool) {
	t.Helper()
	flusher, ok = w.(http.Flusher)
	if !ok {
		msg := "testserver: ResponseWriter does not support flushing"
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	return flusher, true
}

// WriteSSE marshals v as a single "data:" frame and flushes it.
func WriteSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// SSEUnknownScenario reports an unrecognized streaming scenario via t.Errorf
// and writes an SSE comment so the client gets a deterministic (if wrong)
// response instead of a hang. Used after the response headers (200 OK,
// text/event-stream) are already flushed, when it is too late to switch to a
// 500.
func SSEUnknownScenario(t *testing.T, w http.ResponseWriter, flusher http.Flusher, text string) {
	t.Helper()
	t.Errorf("testserver: unknown streaming scenario %q", text)
	_, _ = fmt.Fprintf(w, ": testserver: unknown streaming scenario %q\n\n", text)
	flusher.Flush()
}

// JSONUnknownScenario reports an unrecognized scenario via t.Errorf and
// writes a 500 JSON error response.
func JSONUnknownScenario(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	msg := fmt.Sprintf("testserver: unknown scenario %q", text)
	t.Errorf("%s", msg)
	WriteJSONError(w, 500, msg)
}
