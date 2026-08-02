package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/eventstream"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

// fixtureState records the last request the fixture server saw, so tests
// can assert on wire-level request shape.
type fixtureState struct {
	mu          sync.Mutex
	lastRawBody []byte
	lastRequest converseRequest
	lastAuth    string
	lastDate    string
}

func (fs *fixtureState) record(raw []byte, req converseRequest, r *http.Request) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.lastRawBody = raw
	fs.lastRequest = req
	fs.lastAuth = r.Header.Get("Authorization")
	fs.lastDate = r.Header.Get("X-Amz-Date")
}

func (fs *fixtureState) snapshot() (converseRequest, string, string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastRequest, fs.lastAuth, fs.lastDate
}

func lastUserText(req converseRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Text != nil && *b.Text != "" {
				return *b.Text
			}
		}
	}
	return ""
}

func writeEvent(t *testing.T, w io.Writer, eventType string, payload any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("fixture: marshal event payload: %v", err)
	}
	frame := eventstream.Encode(map[string]string{
		":message-type": "event",
		":event-type":   eventType,
	}, b)
	if _, err := w.Write(frame); err != nil {
		t.Fatalf("fixture: write event frame: %v", err)
	}
}

func writeException(t *testing.T, w io.Writer, excType, message string) {
	t.Helper()
	b, _ := json.Marshal(eventException{Message: message})
	frame := eventstream.Encode(map[string]string{
		":message-type":   "exception",
		":exception-type": excType,
	}, b)
	if _, err := w.Write(frame); err != nil {
		t.Fatalf("fixture: write exception frame: %v", err)
	}
}

func writeTransportError(t *testing.T, w io.Writer, code, message string) {
	t.Helper()
	frame := eventstream.Encode(map[string]string{
		":message-type":  "error",
		":error-code":    code,
		":error-message": message,
	}, nil)
	if _, err := w.Write(frame); err != nil {
		t.Fatalf("fixture: write error frame: %v", err)
	}
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf
}

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureState) {
	t.Helper()
	fs := &fixtureState{}

	mux := http.NewServeMux()

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "AWS4-HMAC-SHA256") || !strings.Contains(got, "SignedHeaders=") {
			t.Errorf("fixture: Authorization header malformed: %q", got)
		}
		if got := r.Header.Get("X-Amz-Date"); got == "" {
			t.Errorf("fixture: X-Amz-Date header missing")
		}

		var req converseRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}
		fs.record(raw, req, r)

		text := lastUserText(req)

		switch text {
		case "fail 429":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(`{"message":"rate limited"}`))
			return
		case "fail 400":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"bad request"}`))
			return
		}

		streaming := strings.HasSuffix(r.URL.Path, "/converse-stream")

		if streaming {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("fixture: ResponseWriter does not support flushing")
			}

			switch text {
			case "stream simple":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				flusher.Flush()
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 0, Delta: eventDeltaUnion{Text: "Hel"}})
				flusher.Flush()
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 0, Delta: eventDeltaUnion{Text: "lo!"}})
				flusher.Flush()
				writeEvent(t, w, "contentBlockStop", eventContentBlockStop{ContentBlockIndex: 0})
				writeEvent(t, w, "messageStop", eventMessageStop{StopReason: "end_turn"})
				writeEvent(t, w, "metadata", eventMetadata{Usage: wireUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}})
				flusher.Flush()

			case "stream tool":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				writeEvent(t, w, "contentBlockStart", eventContentBlockStart{
					ContentBlockIndex: 0,
					Start:             eventContentBlockStartUnion{ToolUse: &eventToolUseStart{ToolUseID: "tu_1", Name: "get_weather"}},
				})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ToolUse: &eventToolUseDelta{Input: `{"city":`}},
				})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ToolUse: &eventToolUseDelta{Input: `"Ghent"}`}},
				})
				writeEvent(t, w, "contentBlockStop", eventContentBlockStop{ContentBlockIndex: 0})
				writeEvent(t, w, "messageStop", eventMessageStop{StopReason: "tool_use"})
				writeEvent(t, w, "metadata", eventMetadata{Usage: wireUsage{InputTokens: 6, OutputTokens: 4, TotalTokens: 10}})
				flusher.Flush()

			case "stream reasoning":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				flusher.Flush()
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ReasoningContent: &eventReasoningContentDelta{Text: "let me "}},
				})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ReasoningContent: &eventReasoningContentDelta{Text: "think..."}},
				})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ReasoningContent: &eventReasoningContentDelta{Signature: "sig-"}},
				})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{
					ContentBlockIndex: 0,
					Delta:             eventDeltaUnion{ReasoningContent: &eventReasoningContentDelta{Signature: "abc"}},
				})
				writeEvent(t, w, "contentBlockStop", eventContentBlockStop{ContentBlockIndex: 0})
				flusher.Flush()
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 1, Delta: eventDeltaUnion{Text: "42"}})
				writeEvent(t, w, "contentBlockStop", eventContentBlockStop{ContentBlockIndex: 1})
				writeEvent(t, w, "messageStop", eventMessageStop{StopReason: "end_turn"})
				writeEvent(t, w, "metadata", eventMetadata{Usage: wireUsage{InputTokens: 7, OutputTokens: 5, TotalTokens: 12}})
				flusher.Flush()

			case "stream truncated":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 0, Delta: eventDeltaUnion{Text: "partial"}})
				flusher.Flush()
				// Connection closes here without contentBlockStop/messageStop.

			case "stream exception":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 0, Delta: eventDeltaUnion{Text: "oops"}})
				writeException(t, w, "internalServerException", "something went wrong")
				flusher.Flush()

			case "stream error":
				writeEvent(t, w, "messageStart", eventMessageStart{Role: "assistant"})
				writeEvent(t, w, "contentBlockDelta", eventContentBlockDelta{ContentBlockIndex: 0, Delta: eventDeltaUnion{Text: "oops"}})
				writeTransportError(t, w, "InternalServerException", "connection reset")
				flusher.Flush()

			default:
				t.Fatalf("fixture: unhandled stream scenario %q", text)
			}
			return
		}

		switch text {
		case "simple":
			writeJSON(t, w, converseResponse{
				Output:     converseOutput{Message: wireMessage{Role: "assistant", Content: []wireContentBlock{{Text: strPtr(fmt.Sprintf("Hello from %s!", providerName))}}}},
				StopReason: "end_turn",
				Usage:      wireUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
			})
		case "tool":
			writeJSON(t, w, converseResponse{
				Output: converseOutput{Message: wireMessage{Role: "assistant", Content: []wireContentBlock{{
					ToolUse: &wireToolUse{ToolUseID: "tu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"Ghent"}`)},
				}}}},
				StopReason: "tool_use",
				Usage:      wireUsage{InputTokens: 6, OutputTokens: 4, TotalTokens: 10},
			})
		default:
			t.Fatalf("fixture: unhandled scenario %q", text)
		}
	}

	mux.HandleFunc("/model/{id}/converse", handler)
	mux.HandleFunc("/model/{id}/converse-stream", handler)

	server := httptest.NewServer(mux)
	return server, fs
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("fixture: encode response: %v", err)
	}
}

func newTestModel(t *testing.T) (provider.LanguageModel, *fixtureState) {
	t.Helper()
	server, fs := newFixtureServer(t)
	t.Cleanup(server.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	return p.Model("anthropic.claude-3-sonnet-20240229-v1:0"), fs
}

func TestProvidertestConformance(t *testing.T) {
	model, _ := newTestModel(t)
	providertest.Run(t, providertest.Config{
		Model:        model,
		ProviderName: providerName,
	})
}

// TestAssistantSourcePartSkippedNotError and
// TestAssistantReasoningPartSkippedNotError cover the spec-owner ruling
// that SDK-generated informational content parts must be replay-safe: a
// SourcePart or ReasoningPart in an assistant message's history must be
// silently skipped when building the next request (not rejected as
// unsupported, and not present on the wire).
func TestAssistantSourcePartSkippedNotError(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "The sky is blue."},
					provider.SourcePart{ID: "source_0", URL: "https://example.com/sky", Title: "Sky Facts"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	blocks := req.Messages[1].Content
	if len(blocks) != 1 || blocks[0].Text == nil || *blocks[0].Text != "The sky is blue." {
		t.Fatalf("blocks = %#v, want exactly one text block (SourcePart dropped)", blocks)
	}
}

// TestAssistantReasoningPartSkippedNotError covers a non-redacted
// ReasoningPart with no Signature: it cannot be replayed as a
// reasoningContent block (Converse requires a signature on a replayed
// reasoningText block, mirroring Anthropic's thinking blocks), so it must
// still be silently skipped, not rejected as unsupported. A signed
// ReasoningPart round-trips instead — see
// TestAssistantRoundTripSignedReasoningFirst.
func TestAssistantReasoningPartSkippedNotError(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.ReasoningPart{Text: "internal reasoning"},
					provider.TextPart{Text: "answer"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	blocks := req.Messages[1].Content
	if len(blocks) != 1 || blocks[0].Text == nil || *blocks[0].Text != "answer" {
		t.Fatalf("blocks = %#v, want exactly one text block (unsigned ReasoningPart dropped)", blocks)
	}
}

// TestAssistantRoundTripSignedReasoningFirst covers that a signed
// (non-redacted) ReasoningPart round-trips as a reasoningContent block, and
// that it leads the assistant turn (mirroring the Anthropic provider's
// convention) even when it isn't first in the original content parts.
func TestAssistantRoundTripSignedReasoningFirst(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "answer"},
					provider.ReasoningPart{Text: "reasoning...", Signature: "sig-1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	blocks := req.Messages[1].Content
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: %#v", len(blocks), blocks)
	}
	rc := blocks[0].ReasoningContent
	if rc == nil || rc.ReasoningText == nil || rc.ReasoningText.Text != "reasoning..." || rc.ReasoningText.Signature != "sig-1" {
		t.Fatalf("blocks[0].ReasoningContent = %#v, want reasoningText{reasoning..., sig-1} first", rc)
	}
	if blocks[1].Text == nil || *blocks[1].Text != "answer" {
		t.Errorf("blocks[1] = %#v, want text/answer", blocks[1])
	}
}

// TestAssistantRoundTripRedactedReasoningFirst covers that a Redacted
// ReasoningPart round-trips as a reasoningContent.redactedContent block,
// also leading the assistant turn.
func TestAssistantRoundTripRedactedReasoningFirst(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "answer"},
					provider.ReasoningPart{Redacted: true, Text: "b64-opaque-blob"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	blocks := req.Messages[1].Content
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: %#v", len(blocks), blocks)
	}
	rc := blocks[0].ReasoningContent
	if rc == nil || rc.RedactedContent != "b64-opaque-blob" {
		t.Fatalf("blocks[0].ReasoningContent = %#v, want redactedContent=b64-opaque-blob first", rc)
	}
}

// TestGenerateReasoningContentRoundTrip covers the non-streaming response
// path: a converseResponse whose message content includes a
// reasoningContent.reasoningText block must decode into a
// provider.ReasoningPart with matching Text/Signature.
func TestGenerateReasoningContentRoundTrip(t *testing.T) {
	server, fs := newFixtureServer(t)
	t.Cleanup(server.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/model/{id}/converse", func(w http.ResponseWriter, r *http.Request) {
		raw := readBody(t, r)
		var req converseRequest
		json.Unmarshal(raw, &req)
		fs.record(raw, req, r)
		writeJSON(t, w, converseResponse{
			Output: converseOutput{Message: wireMessage{Role: "assistant", Content: []wireContentBlock{
				{ReasoningContent: &wireReasoningContent{ReasoningText: &wireReasoningText{Text: "let me think", Signature: "sig-xyz"}}},
				{Text: strPtr("the answer")},
			}}},
			StopReason: "end_turn",
			Usage:      wireUsage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8},
		})
	})
	reasoningServer := httptest.NewServer(mux)
	t.Cleanup(reasoningServer.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", ""),
		WithBaseURL(reasoningServer.URL),
		WithHTTPClient(reasoningServer.Client()),
	)
	model := p.Model("anthropic.claude-3-sonnet-20240229-v1:0")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("Content = %#v, want 2 parts", resp.Content)
	}
	rp, ok := resp.Content[0].(provider.ReasoningPart)
	if !ok {
		t.Fatalf("Content[0] = %T, want ReasoningPart", resp.Content[0])
	}
	if rp.Text != "let me think" || rp.Signature != "sig-xyz" || rp.Redacted {
		t.Fatalf("ReasoningPart = %#v, want {Text: let me think, Signature: sig-xyz, Redacted: false}", rp)
	}
}

func TestRequestShape_ToolsAndInferenceConfig(t *testing.T) {
	model, fs := newTestModel(t)

	maxTokens := 256
	temp := 0.5
	topP := 0.9

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.SystemText("be nice"),
			provider.UserText("tool"),
		},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
		ToolChoice:    &provider.ToolChoice{Mode: provider.ToolChoiceRequired},
		MaxTokens:     &maxTokens,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"STOP"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, auth, date := fs.snapshot()
	if auth == "" || date == "" {
		t.Fatalf("fixture did not record Authorization/X-Amz-Date")
	}

	if len(req.System) != 1 || req.System[0].Text != "be nice" {
		t.Errorf("System = %+v, want [{be nice}]", req.System)
	}
	if req.ToolConfig == nil {
		t.Fatal("ToolConfig is nil, want set")
	}
	if len(req.ToolConfig.Tools) != 1 {
		t.Fatalf("ToolConfig.Tools = %d, want 1", len(req.ToolConfig.Tools))
	}
	spec := req.ToolConfig.Tools[0].ToolSpec
	if spec.Name != "get_weather" {
		t.Errorf("ToolSpec.Name = %q, want get_weather", spec.Name)
	}
	if len(spec.InputSchema.JSON) == 0 {
		t.Errorf("ToolSpec.InputSchema.JSON empty")
	}
	// ToolChoiceRequired maps to Bedrock's {"any":{}}.
	tcBytes, _ := json.Marshal(req.ToolConfig.ToolChoice)
	if string(tcBytes) != `{"any":{}}` {
		t.Errorf("ToolChoice = %s, want {\"any\":{}}", tcBytes)
	}

	if req.InferenceConfig == nil {
		t.Fatal("InferenceConfig is nil, want set")
	}
	if req.InferenceConfig.MaxTokens == nil || *req.InferenceConfig.MaxTokens != 256 {
		t.Errorf("InferenceConfig.MaxTokens = %v, want 256", req.InferenceConfig.MaxTokens)
	}
	if req.InferenceConfig.Temperature == nil || *req.InferenceConfig.Temperature != 0.5 {
		t.Errorf("InferenceConfig.Temperature = %v, want 0.5", req.InferenceConfig.Temperature)
	}
	if req.InferenceConfig.TopP == nil || *req.InferenceConfig.TopP != 0.9 {
		t.Errorf("InferenceConfig.TopP = %v, want 0.9", req.InferenceConfig.TopP)
	}
	if len(req.InferenceConfig.StopSequences) != 1 || req.InferenceConfig.StopSequences[0] != "STOP" {
		t.Errorf("InferenceConfig.StopSequences = %v, want [STOP]", req.InferenceConfig.StopSequences)
	}
}

func TestRequestShape_ToolChoiceAutoAndTool(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("tool")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceAuto},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req, _, _ := fs.snapshot()
	tcBytes, _ := json.Marshal(req.ToolConfig.ToolChoice)
	if string(tcBytes) != `{"auto":{}}` {
		t.Errorf("ToolChoiceAuto = %s, want {\"auto\":{}}", tcBytes)
	}

	_, err = model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("tool")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req, _, _ = fs.snapshot()
	tcBytes, _ = json.Marshal(req.ToolConfig.ToolChoice)
	if string(tcBytes) != `{"tool":{"name":"get_weather"}}` {
		t.Errorf("ToolChoiceTool = %s, want {\"tool\":{\"name\":\"get_weather\"}}", tcBytes)
	}
}

func TestRequestShape_ToolChoiceNoneOmitsToolConfig(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceNone},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req, _, _ := fs.snapshot()
	if req.ToolConfig != nil {
		t.Errorf("ToolConfig = %+v, want nil (ToolChoiceNone omits toolConfig entirely)", req.ToolConfig)
	}
}

// TestRequestShape_ToolChoiceWithoutToolsErrors covers the guard against a
// toolChoice-only toolConfig: Bedrock rejects a toolConfig that carries a
// toolChoice but no tools, so buildConverseRequest (via Generate) must fail
// fast with a descriptive error instead of sending that request.
func TestRequestShape_ToolChoiceWithoutToolsErrors(t *testing.T) {
	model, _ := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceAuto},
	})
	if err == nil {
		t.Fatal("Generate: want error for ToolChoice set without Tools, got nil")
	}
	if !strings.Contains(err.Error(), "tool choice requires at least one tool") {
		t.Errorf("Generate error = %v, want it to mention tool choice requiring at least one tool", err)
	}
}

func TestRequestShape_ToolResultErrorStatus(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{Role: provider.RoleAssistant, Content: []provider.ContentPart{
				provider.ToolCallPart{ID: "tu_1", Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
			}},
			{Role: provider.RoleTool, Content: []provider.ContentPart{
				provider.ToolResultPart{ToolCallID: "tu_1", Name: "get_weather", Result: "boom", IsError: true},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req, _, _ := fs.snapshot()

	var found *wireToolResult
	for _, m := range req.Messages {
		for _, c := range m.Content {
			if c.ToolResult != nil {
				found = c.ToolResult
			}
		}
	}
	if found == nil {
		t.Fatal("no toolResult content block found in request")
	}
	if found.Status != "error" {
		t.Errorf("toolResult.Status = %q, want %q", found.Status, "error")
	}
	if found.ToolUseID != "tu_1" {
		t.Errorf("toolResult.ToolUseID = %q, want tu_1", found.ToolUseID)
	}
}

// TestRequestShapeUserMessageFilePartPDF verifies a PDF FilePart becomes a
// Converse "document" content block with format "pdf", Name derived from
// Filename (sans extension), and the base64 bytes carried the same way an
// image source is (wireImageSource's {"bytes": ...} shape).
func TestRequestShapeUserMessageFilePartPDF(t *testing.T) {
	model, fs := newTestModel(t)

	pdfData := []byte("%PDF-1.4 fake pdf bytes")
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "simple"},
					provider.FilePart{Data: pdfData, MediaType: "application/pdf", Filename: "report.pdf"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("Messages = %+v, want 1 message with 2 content blocks", req.Messages)
	}
	doc := req.Messages[0].Content[1].Document
	if doc == nil {
		t.Fatalf("Content[1].Document = nil, want set")
	}
	if doc.Format != "pdf" {
		t.Errorf("Document.Format = %q, want pdf", doc.Format)
	}
	if doc.Name != "report" {
		t.Errorf("Document.Name = %q, want report (extension stripped)", doc.Name)
	}
	wantBytes := base64.StdEncoding.EncodeToString(pdfData)
	if doc.Source.Bytes != wantBytes {
		t.Errorf("Document.Source.Bytes = %q, want %q", doc.Source.Bytes, wantBytes)
	}
}

// TestRequestShapeUserMessageFilePartCSVDefaultName verifies a non-PDF
// supported document type (CSV) maps to format "csv", and that an empty
// Filename falls back to the "document" default name Converse requires.
func TestRequestShapeUserMessageFilePartCSVDefaultName(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "simple"},
					provider.FilePart{Data: []byte("a,b,c\n1,2,3\n"), MediaType: "text/csv"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	doc := req.Messages[0].Content[1].Document
	if doc == nil {
		t.Fatalf("Content[1].Document = nil, want set")
	}
	if doc.Format != "csv" {
		t.Errorf("Document.Format = %q, want csv", doc.Format)
	}
	if doc.Name != "document" {
		t.Errorf("Document.Name = %q, want the default %q", doc.Name, "document")
	}
}

// TestDocumentName covers documentName's sanitization: Converse's
// document.name field only allows alphanumerics, whitespace, hyphens,
// parentheses, and brackets, so anything else (e.g. the underscores and
// extra '.' left after extension-stripping "my_report_v2.1.pdf") must be
// replaced by a space, with runs of whitespace collapsed and the result
// trimmed — not passed through raw, which Converse would reject live.
func TestDocumentName(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"my_report_v2.1.pdf", "my report v2 1"},
		{"Annual Report (2024).pdf", "Annual Report (2024)"},
		{"", "document"},
		{"___.pdf", "document"},
		{"safe-name[1].csv", "safe-name[1]"},
	}
	for _, c := range cases {
		t.Run(c.filename, func(t *testing.T) {
			if got := documentName(c.filename); got != c.want {
				t.Errorf("documentName(%q) = %q, want %q", c.filename, got, c.want)
			}
		})
	}
}

// TestRequestShapeUserMessageFilePartSanitizesDisallowedNameChars verifies
// the sanitization in TestDocumentName is actually applied along the full
// Generate path, not just at the unit level.
func TestRequestShapeUserMessageFilePartSanitizesDisallowedNameChars(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "simple"},
					provider.FilePart{Data: []byte("data"), MediaType: "application/pdf", Filename: "my_report_v2.1.pdf"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req, _, _ := fs.snapshot()
	doc := req.Messages[0].Content[1].Document
	if doc == nil {
		t.Fatalf("Content[1].Document = nil, want set")
	}
	if doc.Name != "my report v2 1" {
		t.Errorf("Document.Name = %q, want %q", doc.Name, "my report v2 1")
	}
}

// TestRequestShapeUserMessageFilePartUnsupportedTypeErrors verifies a
// FilePart with a MediaType outside Converse's fixed document-format set
// (e.g. audio) is rejected with a descriptive error rather than silently
// defaulting to some format, unlike imageFormat's PNG fallback.
func TestRequestShapeUserMessageFilePartUnsupportedTypeErrors(t *testing.T) {
	model, _ := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "simple"},
					provider.FilePart{Data: []byte("data"), MediaType: "audio/mpeg"},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Generate: want error for unsupported FilePart media type, got nil")
	}
	if !strings.Contains(err.Error(), "audio/mpeg") {
		t.Errorf("error = %q, want it to mention the unsupported media type", err.Error())
	}
}

func TestStream_TruncatedWithoutMessageStop(t *testing.T) {
	model, _ := newTestModel(t)

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream truncated")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes int
	for part := range sr.Parts() {
		if _, ok := part.(provider.FinishPart); ok {
			finishes++
		}
	}
	if finishes != 0 {
		t.Errorf("got %d FinishPart(s) for a truncated stream, want 0", finishes)
	}
	if sr.Err() == nil {
		t.Fatal("Err() = nil, want a truncation error")
	}
}

func TestStream_ExceptionFrame(t *testing.T) {
	model, _ := newTestModel(t)

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream exception")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes int
	for part := range sr.Parts() {
		if _, ok := part.(provider.FinishPart); ok {
			finishes++
		}
	}
	if finishes != 0 {
		t.Errorf("got %d FinishPart(s) for an exception stream, want 0", finishes)
	}
	if sr.Err() == nil {
		t.Fatal("Err() = nil, want the exception surfaced via Err()")
	}
	if !strings.Contains(sr.Err().Error(), "internalServerException") {
		t.Errorf("Err() = %v, want it to mention the exception type", sr.Err())
	}
}

// TestStream_TransportErrorFrame covers a ":message-type": "error" event
// stream frame — a transport-level error distinct from the modeled
// ":message-type": "exception" case — carrying its details in the
// ":error-code" / ":error-message" headers rather than a JSON payload. Err()
// must surface a descriptive error mentioning both, and iteration must stop
// without emitting a FinishPart.
func TestStream_TransportErrorFrame(t *testing.T) {
	model, _ := newTestModel(t)

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream error")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes int
	for part := range sr.Parts() {
		if _, ok := part.(provider.FinishPart); ok {
			finishes++
		}
	}
	if finishes != 0 {
		t.Errorf("got %d FinishPart(s) for a transport-error stream, want 0", finishes)
	}
	if sr.Err() == nil {
		t.Fatal("Err() = nil, want the transport error surfaced via Err()")
	}
	if !strings.Contains(sr.Err().Error(), "InternalServerException") {
		t.Errorf("Err() = %v, want it to mention the error code", sr.Err())
	}
	if !strings.Contains(sr.Err().Error(), "connection reset") {
		t.Errorf("Err() = %v, want it to mention the error message", sr.Err())
	}
}

func TestModelPath_URLEscapesModelID(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeJSON(t, w, converseResponse{
			Output:     converseOutput{Message: wireMessage{Role: "assistant", Content: []wireContentBlock{{Text: strPtr("hi")}}}},
			StopReason: "end_turn",
			Usage:      wireUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		})
	}))
	defer server.Close()

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKID", "secret", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	model := p.Model("anthropic.claude-3-sonnet-20240229-v1:0")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	const want = "/model/anthropic.claude-3-sonnet-20240229-v1%3A0/converse"
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

// TestNew_RegionEnvFallback covers the AWS_REGION / AWS_DEFAULT_REGION
// precedence used when no WithRegion option is given: AWS_REGION wins when
// both are set, AWS_DEFAULT_REGION is honored when only it is set, and the
// hard-coded default applies when neither is set.
func TestNew_RegionEnvFallback(t *testing.T) {
	t.Run("AWS_REGION wins over AWS_DEFAULT_REGION", func(t *testing.T) {
		t.Setenv("AWS_REGION", "eu-west-1")
		t.Setenv("AWS_DEFAULT_REGION", "ap-southeast-2")
		p := New()
		if p.region != "eu-west-1" {
			t.Errorf("region = %q, want %q", p.region, "eu-west-1")
		}
	})

	t.Run("AWS_DEFAULT_REGION used when AWS_REGION unset", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		t.Setenv("AWS_DEFAULT_REGION", "ap-southeast-2")
		p := New()
		if p.region != "ap-southeast-2" {
			t.Errorf("region = %q, want %q", p.region, "ap-southeast-2")
		}
	})

	t.Run("hard-coded default when neither is set", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		t.Setenv("AWS_DEFAULT_REGION", "")
		p := New()
		if p.region != defaultRegion {
			t.Errorf("region = %q, want %q", p.region, defaultRegion)
		}
	})
}

// TestConvertResponse_EmptyTextBlockNotDropped covers a bug where
// convertResponse discriminated content blocks with `block.Text != ""`: a
// text block whose text happens to be the empty string is a legitimate,
// present block (e.g. {"text":""}), not the absence of one, and must still
// surface as an (empty) provider.TextPart rather than being silently
// dropped from the response.
func TestConvertResponse_EmptyTextBlockNotDropped(t *testing.T) {
	wr := converseResponse{
		Output: converseOutput{Message: wireMessage{
			Role:    "assistant",
			Content: []wireContentBlock{{Text: strPtr("")}},
		}},
		StopReason: "end_turn",
		Usage:      wireUsage{InputTokens: 1, OutputTokens: 0, TotalTokens: 1},
	}

	resp := convertResponse(wr, nil)

	if len(resp.Content) != 1 {
		t.Fatalf("Content = %d parts, want 1 (empty text block must not be dropped)", len(resp.Content))
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok {
		t.Fatalf("Content[0] = %T, want provider.TextPart", resp.Content[0])
	}
	if tp.Text != "" {
		t.Errorf("TextPart.Text = %q, want empty string", tp.Text)
	}
}

// TestStream_ReasoningContent covers the streaming reasoningContent path:
// text deltas accumulate into ReasoningDelta parts, signature fragments
// accumulate silently (no stream part of their own), and contentBlockStop
// emits a ReasoningEnd carrying the fully assembled ReasoningPart
// (Text/Signature), followed by the ordinary text content block and a
// FinishPart.
func TestStream_ReasoningContent(t *testing.T) {
	model, _ := newTestModel(t)

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream reasoning")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var reasoningDeltas []string
	var reasoningEnds []provider.ReasoningPart
	var text string
	var finishes int
	for part := range sr.Parts() {
		switch p := part.(type) {
		case provider.ReasoningDelta:
			reasoningDeltas = append(reasoningDeltas, p.Text)
		case provider.ReasoningEnd:
			reasoningEnds = append(reasoningEnds, p.Part)
		case provider.TextDelta:
			text += p.Text
		case provider.FinishPart:
			finishes++
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err(): %v", err)
	}
	if finishes != 1 {
		t.Fatalf("finishes = %d, want 1", finishes)
	}
	if len(reasoningDeltas) != 2 || reasoningDeltas[0] != "let me " || reasoningDeltas[1] != "think..." {
		t.Fatalf("reasoningDeltas = %#v", reasoningDeltas)
	}
	if len(reasoningEnds) != 1 {
		t.Fatalf("reasoningEnds = %d, want 1", len(reasoningEnds))
	}
	end := reasoningEnds[0]
	if end.Text != "let me think..." || end.Signature != "sig-abc" || end.Redacted {
		t.Fatalf("ReasoningEnd.Part = %#v, want {Text: \"let me think...\", Signature: sig-abc, Redacted: false}", end)
	}
	if text != "42" {
		t.Fatalf("text = %q, want 42", text)
	}
}

func TestErrors_As_APICallError(t *testing.T) {
	model, _ := newTestModel(t)
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("fail 400")},
	})
	if err == nil {
		t.Fatal("want error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *ai.APICallError", err, err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
