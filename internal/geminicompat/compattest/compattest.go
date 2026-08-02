// Package compattest provides a shared httptest fixture server that speaks
// the Gemini generateContent/streamGenerateContent/batchEmbedContents wire
// format, for use by provider/providertest conformance runs and other tests
// of internal/geminicompat-based providers.
//
// It intentionally does not import internal/geminicompat's unexported wire
// types: it defines its own minimal mirror of the wire shapes it needs to
// produce, which keeps it usable as a true black-box fixture of "whatever
// speaks the Gemini wire format" rather than being coupled to geminicompat's
// internals.
package compattest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ---- wire types (request side, just enough to decode) ----

type wirePart struct {
	Text string `json:"text,omitempty"`
}

type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

type generateContentRequest struct {
	Contents []wireContent `json:"contents"`
}

type embedContentRequest struct {
	Model   string      `json:"model"`
	Content wireContent `json:"content"`
}

type batchEmbedRequest struct {
	Requests []embedContentRequest `json:"requests"`
}

// ---- wire types (response side) ----

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type wireResponsePart struct {
	Text         string            `json:"text,omitempty"`
	FunctionCall *wireFunctionCall `json:"functionCall,omitempty"`
}

type wireResponseContent struct {
	Role  string             `json:"role,omitempty"`
	Parts []wireResponsePart `json:"parts"`
}

type wireCandidate struct {
	Content      wireResponseContent `json:"content"`
	FinishReason string              `json:"finishReason,omitempty"`
}

type wireUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type generateContentResponse struct {
	Candidates    []wireCandidate    `json:"candidates"`
	UsageMetadata *wireUsageMetadata `json:"usageMetadata,omitempty"`
}

type embeddingValues struct {
	Values []float64 `json:"values"`
}

type batchEmbedResponse struct {
	Embeddings []embeddingValues `json:"embeddings"`
}

// Server is a fixture httptest.Server speaking the Gemini
// generateContent/streamGenerateContent/batchEmbedContents wire format.
// Callers must Close it (or rely on the t.Cleanup registered by
// NewFixtureServer).
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	requests [][]byte
	headers  []http.Header
}

// Requests returns the raw JSON bodies of every generateContent /
// streamGenerateContent / batchEmbedContents request received so far, in
// arrival order.
func (s *Server) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.requests))
	copy(out, s.requests)
	return out
}

// HeaderValues returns the named header's value for every request received
// so far, in arrival order (one entry per Requests() entry). Missing
// headers yield "" for that entry.
func (s *Server) HeaderValues(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.headers))
	for i, h := range s.headers {
		out[i] = h.Get(name)
	}
	return out
}

func (s *Server) record(raw []byte, header http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, raw)
	s.headers = append(s.headers, header)
}

// readBody reads r's body, reporting via t.Errorf and writing a 500
// response on failure. It returns ok=false when the caller (a handler
// running on its own goroutine) should stop processing the request; t.Fatalf
// is not goroutine-safe per the testing docs, so handler code must use this
// pattern instead of failing the test directly.
func readBody(t *testing.T, w http.ResponseWriter, r *http.Request) (buf []byte, ok bool) {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		msg := fmt.Sprintf("compattest: read body: %v", err)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return nil, false
	}
	return buf, true
}

// lastUserText extracts the text of the last "user"-role content's first
// text part, which the fixtures below always send as a single text part.
func lastUserText(req generateContentRequest) string {
	for i := len(req.Contents) - 1; i >= 0; i-- {
		c := req.Contents[i]
		if c.Role != "user" {
			continue
		}
		for _, p := range c.Parts {
			if p.Text != "" {
				return p.Text
			}
		}
	}
	return ""
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// NewFixtureServer returns an httptest.Server speaking the Gemini
// generateContent/streamGenerateContent/batchEmbedContents wire format,
// implementing the six providertest scenarios keyed off the LAST user
// message text, with "simple" responding "Hello from <providerName>!".
// Callers must Close it (handled automatically via t.Cleanup).
func NewFixtureServer(t *testing.T, providerName string) *Server {
	t.Helper()
	s := &Server{}

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got == "" {
			t.Errorf("compattest: missing x-goog-api-key header")
		}

		switch {
		case strings.HasSuffix(r.URL.Path, ":batchEmbedContents"):
			handleEmbed(t, s, w, r)
		case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
			handleGenerate(t, s, w, r, providerName, true)
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			handleGenerate(t, s, w, r, providerName, false)
		default:
			msg := fmt.Sprintf("compattest: unexpected path %q", r.URL.Path)
			t.Errorf("%s", msg)
			http.Error(w, msg, 404)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	s.Server = srv
	return s
}

func handleGenerate(t *testing.T, s *Server, w http.ResponseWriter, r *http.Request, providerName string, stream bool) {
	t.Helper()

	if stream != (r.URL.Query().Get("alt") == "sse") {
		t.Errorf("compattest: alt=sse query param mismatch for path %q (stream=%v)", r.URL.Path, stream)
	}

	var req generateContentRequest
	raw, ok := readBody(t, w, r)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		msg := fmt.Sprintf("compattest: decode request: %v", err)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return
	}
	s.record(raw, r.Header.Clone())

	text := lastUserText(req)

	switch text {
	case "fail 429":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
		return
	case "fail 400":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
		return
	}

	if stream {
		flusher, flushOK := w.(http.Flusher)
		if !flushOK {
			msg := "compattest: ResponseWriter does not support flushing"
			t.Errorf("%s", msg)
			http.Error(w, msg, 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		switch text {
		case "stream simple":
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content: wireResponseContent{Role: "model", Parts: []wireResponsePart{{Text: "Hel"}}},
				}},
			})
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content:      wireResponseContent{Role: "model", Parts: []wireResponsePart{{Text: "lo!"}}},
					FinishReason: "STOP",
				}},
				UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5},
			})
		case "stream tool":
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content: wireResponseContent{Role: "model", Parts: []wireResponsePart{{
						FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
					}}},
					FinishReason: "STOP",
				}},
				UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
			})
		default:
			// Response headers (200 OK, text/event-stream) are already
			// flushed by this point, so we can't switch to a 500; just
			// report the failure and write an SSE comment so the client
			// gets a deterministic (if wrong) response instead of a hang.
			t.Errorf("compattest: unknown streaming scenario %q", text)
			fmt.Fprintf(w, ": compattest: unknown streaming scenario %q\n\n", text)
			flusher.Flush()
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch text {
	case "simple":
		resp := generateContentResponse{
			Candidates: []wireCandidate{{
				Content:      wireResponseContent{Role: "model", Parts: []wireResponsePart{{Text: fmt.Sprintf("Hello from %s!", providerName)}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 3, TotalTokenCount: 8},
		}
		json.NewEncoder(w).Encode(resp)
	case "tool":
		resp := generateContentResponse{
			Candidates: []wireCandidate{{
				Content: wireResponseContent{Role: "model", Parts: []wireResponsePart{{
					FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
				}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
		}
		json.NewEncoder(w).Encode(resp)
	default:
		msg := fmt.Sprintf("compattest: unknown scenario %q", text)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
	}
}

func handleEmbed(t *testing.T, s *Server, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	var req batchEmbedRequest
	raw, ok := readBody(t, w, r)
	if !ok {
		return
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		msg := fmt.Sprintf("compattest: decode embed request: %v", err)
		t.Errorf("%s", msg)
		http.Error(w, msg, 500)
		return
	}
	s.record(raw, r.Header.Clone())

	embeddings := make([]embeddingValues, len(req.Requests))
	for i, rq := range req.Requests {
		text := ""
		if len(rq.Content.Parts) > 0 {
			text = rq.Content.Parts[0].Text
		}
		embeddings[i] = embeddingValues{Values: []float64{float64(i), float64(len(text))}}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batchEmbedResponse{Embeddings: embeddings})
}
