package geminicompat

// TestToolLoopSecondRequestSucceedsAfterGroundedStep is an integration-style
// test for the spec-owner ruling that SDK-generated informational content
// parts (SourcePart, ReasoningPart) must be replay-safe: a step whose
// response carries BOTH grounding sources (SourcePart, from groundingMetadata)
// AND a tool call triggers ai.GenerateText's tool loop to resend that step's
// assistant message (including the SourcePart) on the SECOND request. Before
// the fix, assistantParts would reject the SourcePart as an unsupported
// content part and the second request would never even go out; this test
// asserts the loop completes and that the second request body carries no
// grounding artifacts (the SourcePart was dropped on the wire, not
// replayed).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func TestToolLoopSecondRequestSucceedsAfterGroundedStep(t *testing.T) {
	var mu sync.Mutex
	var rawBodies [][]byte
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()

		body := readAll(t, r)
		mu.Lock()
		rawBodies = append(rawBodies, body)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if n == 1 {
			// First step: grounded text AND a tool call in the same
			// response — the realistic case that produces an assistant
			// message with SourcePart + ToolCallPart content parts.
			wr := generateContentResponse{
				Candidates: []wireCandidate{{
					Content: wireContent{Parts: []wirePart{
						{Text: "The sky is blue."},
						{FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)}},
					}},
					GroundingMetadata: &wireGroundingMeta{GroundingChunks: []wireGroundingChunk{
						{Web: &wireGroundingWeb{URI: "https://example.com/sky", Title: "Sky Facts"}},
					}},
					FinishReason: "STOP",
				}},
			}
			json.NewEncoder(w).Encode(wr)
			return
		}

		// Second step: the tool loop resent the first step's assistant
		// message (with SourcePart dropped) plus the tool result; respond
		// with a plain final answer.
		wr := generateContentResponse{
			Candidates: []wireCandidate{{
				Content:      wireContent{Parts: []wirePart{{Text: "It's sunny in Ghent."}}},
				FinishReason: "STOP",
			}},
		}
		json.NewEncoder(w).Encode(wr)
	}))
	defer srv.Close()

	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	weatherTool := ai.NewTool("get_weather", "Get the current weather for a city",
		func(ctx context.Context, args struct {
			City string `json:"city"`
		}) (any, error) {
			return map[string]string{"forecast": "sunny"}, nil
		})

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "why is the sky blue, and what's the weather in Ghent",
		Tools:    []ai.Tool{weatherTool},
		MaxSteps: 2,
	})
	if err != nil {
		t.Fatalf("GenerateText: %v (before the fix, the second request would have failed with 'unsupported content part')", err)
	}

	if len(res.Steps) != 2 {
		t.Fatalf("got %d step(s), want 2: %+v", len(res.Steps), res.Steps)
	}
	if len(res.Steps[0].Sources) != 1 || res.Steps[0].Sources[0].URL != "https://example.com/sky" {
		t.Fatalf("Steps[0].Sources = %#v", res.Steps[0].Sources)
	}
	if res.Text != "It's sunny in Ghent." {
		t.Fatalf("Text = %q", res.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(rawBodies) != 2 {
		t.Fatalf("got %d request(s), want 2", len(rawBodies))
	}
	second := string(rawBodies[1])
	if strings.Contains(second, "example.com/sky") || strings.Contains(second, "Sky Facts") || strings.Contains(second, "groundingMetadata") {
		t.Errorf("second request body contains grounding artifacts, want dropped: %s", second)
	}
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}
