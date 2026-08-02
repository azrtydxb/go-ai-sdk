package vertex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/gauth"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

// ---- fixture server ----
//
// Vertex's generateContent/streamGenerateContent wire format is identical
// to plain Gemini's (see internal/geminicompat/compattest), just served at
// a different URL shape and authenticated via "Authorization: Bearer
// <token>" instead of "x-goog-api-key". This fixture is a minimal
// black-box mirror of that wire format at Vertex-style paths, and also
// asserts the exact path shape and bearer auth header vertex.go must
// produce.

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

const (
	testProject  = "test-project"
	testLocation = "us-central1"
	testModel    = "gemini-test"
)

// wantPathSuffix is the exact URL path shape vertex.go's EndpointFor must
// produce, minus the fixture server's base URL.
func wantPathSuffix(method string) string {
	return fmt.Sprintf("/projects/%s/locations/%s/publishers/google/models/%s:%s", testProject, testLocation, testModel, method)
}

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

func newFixtureServer(t *testing.T, wantBearer string) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer "+wantBearer)
		}

		switch {
		case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
			if got, want := r.URL.Path, wantPathSuffix("streamGenerateContent"); got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			handleGenerate(t, w, r, true)
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			if got, want := r.URL.Path, wantPathSuffix("generateContent"); got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			handleGenerate(t, w, r, false)
		default:
			http.Error(w, fmt.Sprintf("unexpected path %q", r.URL.Path), 404)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func handleGenerate(t *testing.T, w http.ResponseWriter, r *http.Request, stream bool) {
	t.Helper()

	if stream != (r.URL.Query().Get("alt") == "sse") {
		t.Errorf("alt=sse query param mismatch for path %q (stream=%v)", r.URL.Path, stream)
	}

	var req generateContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
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
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "ResponseWriter does not support flushing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		switch text {
		case "stream simple":
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{Content: wireResponseContent{Role: "model", Parts: []wireResponsePart{{Text: "Hel"}}}}},
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
			fmt.Fprintf(w, ": unknown streaming scenario %q\n\n", text)
			flusher.Flush()
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch text {
	case "simple":
		json.NewEncoder(w).Encode(generateContentResponse{
			Candidates: []wireCandidate{{
				Content:      wireResponseContent{Role: "model", Parts: []wireResponsePart{{Text: "Hello from vertex!"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 3, TotalTokenCount: 8},
		})
	case "tool":
		json.NewEncoder(w).Encode(generateContentResponse{
			Candidates: []wireCandidate{{
				Content: wireResponseContent{Role: "model", Parts: []wireResponsePart{{
					FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
				}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
		})
	default:
		http.Error(w, fmt.Sprintf("unknown scenario %q", text), 500)
	}
}

func newTestModel(t *testing.T) (provider.LanguageModel, *httptest.Server) {
	t.Helper()
	const token = "static-test-token"
	srv := newFixtureServer(t, token)
	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL(srv.URL),
		WithAccessToken(token),
	)
	return p.Model(testModel), srv
}

func TestConformance(t *testing.T) {
	model, _ := newTestModel(t)
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "vertex"})
}

func TestURLPathShape(t *testing.T) {
	model, _ := newTestModel(t)
	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Text() != "Hello from vertex!" {
		t.Errorf("Text() = %q, want %q", resp.Text(), "Hello from vertex!")
	}
}

func TestWithTokenSource(t *testing.T) {
	const token = "from-token-source"
	srv := newFixtureServer(t, token)
	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL(srv.URL),
		WithTokenSource(gauth.StaticTokenSource(token)),
	)
	model := p.Model(testModel)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

func TestDefaultLocation(t *testing.T) {
	p := New(WithProject(testProject))
	if p.location != "us-central1" {
		t.Errorf("default location = %q, want %q", p.location, "us-central1")
	}
}

func TestNoCredentialsConfigured(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	p := New(WithProject(testProject), WithLocation(testLocation), WithBaseURL("http://example.invalid"))
	model := p.Model(testModel)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err == nil {
		t.Fatal("Generate: want error, got nil")
	}
	if !strings.Contains(err.Error(), "vertex: no credentials configured") {
		t.Errorf("error = %v, want it to mention %q", err, "vertex: no credentials configured")
	}
}

func TestCancel(t *testing.T) {
	model, _ := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := model.Generate(ctx, provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
	}
}
