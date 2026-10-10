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
//
// The generic recording-server scaffolding (recording log, body reading,
// SSE framing, canned error scenarios) lives in internal/testserver; this
// package adds only the Gemini wire-format handlers and scenario mapping.
package compattest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/testserver"
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
// generateContent/streamGenerateContent/batchEmbedContents wire format. It
// reuses the recording log from internal/testserver: Requests() returns
// every request body in arrival order, HeaderValues(name) the matching
// header values. Callers must Close it (or rely on the t.Cleanup registered
// by NewFixtureServer).
type Server = testserver.Server

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

// writeSSE frames v as a single "data:" SSE event and flushes — a thin
// wrapper over internal/testserver keeping the handler bodies below
// readable.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	testserver.WriteSSE(w, flusher, v)
}

// NewFixtureServer returns an httptest.Server speaking the Gemini
// generateContent/streamGenerateContent/batchEmbedContents wire format,
// implementing the six providertest scenarios keyed off the LAST user
// message text, with "simple" responding "Hello from <providerName>!".
// Callers must Close it (handled automatically via t.Cleanup).
func NewFixtureServer(t *testing.T, providerName string) *Server {
	t.Helper()

	var sPtr atomic.Pointer[testserver.Server]

	handler := func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
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

	s := testserver.New(t, http.HandlerFunc(handler))
	sPtr.Store(s)
	return s
}

func handleGenerate(t *testing.T, s *Server, w http.ResponseWriter, r *http.Request, providerName string, stream bool) {
	t.Helper()

	if stream != (r.URL.Query().Get("alt") == "sse") {
		t.Errorf("compattest: alt=sse query param mismatch for path %q (stream=%v)", r.URL.Path, stream)
	}

	var req generateContentRequest
	if !testserver.DecodeAndRecord(t, s, w, r, &req) {
		return
	}

	text := lastUserText(req)

	if testserver.ErrorScenario(w, text) {
		return
	}

	if stream {
		flusher, ok := testserver.StartSSE(t, w)
		if !ok {
			return
		}

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
			testserver.SSEUnknownScenario(t, w, flusher, text)
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
		_ = json.NewEncoder(w).Encode(resp)
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
		_ = json.NewEncoder(w).Encode(resp)
	default:
		testserver.JSONUnknownScenario(t, w, text)
	}
}

func handleEmbed(t *testing.T, s *Server, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	var req batchEmbedRequest
	if !testserver.DecodeAndRecord(t, s, w, r, &req) {
		return
	}

	embeddings := make([]embeddingValues, len(req.Requests))
	for i, rq := range req.Requests {
		text := ""
		if len(rq.Content.Parts) > 0 {
			text = rq.Content.Parts[0].Text
		}
		embeddings[i] = embeddingValues{Values: []float64{float64(i), float64(len(text))}}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(batchEmbedResponse{Embeddings: embeddings})
}
