package bedrock

import (
	"context"
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
