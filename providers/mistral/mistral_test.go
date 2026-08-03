package mistral

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

// fixtureState records the last request the fixture server saw, so tests
// can assert on wire-level request shape.
type fixtureState struct {
	mu          sync.Mutex
	lastRawBody []byte
	lastRequest chatRequest
}

func (fs *fixtureState) record(raw []byte, req chatRequest) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.lastRawBody = raw
	fs.lastRequest = req
}

func (fs *fixtureState) rawBody() []byte {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastRawBody
}

func (fs *fixtureState) request() chatRequest {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastRequest
}

// lastUserText extracts the text of the last user message, which the
// fixtures below always send as a plain JSON string.
func lastUserText(req chatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		var text string
		if err := json.Unmarshal(m.Content, &text); err == nil {
			return text
		}
	}
	return ""
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureState) {
	t.Helper()
	fs := &fixtureState{}

	mux := http.NewServeMux()

	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("fixture: Authorization header = %q, want Bearer prefix", got)
		}

		var req chatRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}
		fs.record(raw, req)

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

		if req.Stream {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("fixture: ResponseWriter does not support flushing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			switch text {
			case "stream simple":
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}})
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "lo!"}}}})
				stop := "stop"
				writeSSE(w, flusher, chatStreamChunk{
					Choices: []chatStreamChoice{{FinishReason: &stop}},
					Usage:   &wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
				})
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			case "stream tool":
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
					ToolCalls: []wireToolCallDelta{{Index: 0, ID: "call_1", Type: "function", Function: wireToolCallFunc{Name: "get_weather"}}},
				}}}})
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
					ToolCalls: []wireToolCallDelta{{Index: 0, Function: wireToolCallFunc{Arguments: `{"city":`}}},
				}}}})
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
					ToolCalls: []wireToolCallDelta{{Index: 0, Function: wireToolCallFunc{Arguments: `"Ghent"}`}}},
				}}}})
				toolCalls := "tool_calls"
				writeSSE(w, flusher, chatStreamChunk{
					Choices: []chatStreamChoice{{FinishReason: &toolCalls}},
					Usage:   &wireUsage{PromptTokens: 6, CompletionTokens: 4, TotalTokens: 10},
				})
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			default:
				t.Fatalf("fixture: unknown streaming scenario %q", text)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch text {
		case "simple":
			content := "Hello from mistral!"
			resp := chatResponse{
				Choices: []chatResponseChoice{{
					Message:      chatResponseMessage{Content: &content},
					FinishReason: "stop",
				}},
				Usage: wireUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			}
			json.NewEncoder(w).Encode(resp)
		case "tool":
			resp := chatResponse{
				Choices: []chatResponseChoice{{
					Message: chatResponseMessage{
						ToolCalls: []wireToolCall{{
							ID:       "call_1",
							Type:     "function",
							Function: wireToolCallFunc{Name: "get_weather", Arguments: `{"city":"Ghent"}`},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: wireUsage{PromptTokens: 6, CompletionTokens: 4, TotalTokens: 10},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("fixture: unknown scenario %q", text)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fs
}

func TestConformance(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("mistral-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "mistral"})
}

func TestCapabilities(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")
	if caps := model.Capabilities(); !caps.NativeJSON {
		t.Errorf("Capabilities().NativeJSON = false, want true")
	}
}

// TestAssistantSourcePartSkippedNotError and
// TestAssistantReasoningPartSkippedNotError cover the spec-owner ruling
// that SDK-generated informational content parts must be replay-safe: a
// SourcePart or ReasoningPart in an assistant message's history must be
// silently skipped when building the next request (not rejected as
// unsupported, and not present on the wire).
func TestAssistantSourcePartSkippedNotError(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

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
	if strings.Contains(string(fs.rawBody()), "example.com/sky") || strings.Contains(string(fs.rawBody()), "Sky Facts") {
		t.Errorf("request body contains grounding artifacts, want dropped: %s", fs.rawBody())
	}
}

func TestAssistantReasoningPartSkippedNotError(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

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
	if strings.Contains(string(fs.rawBody()), "internal reasoning") {
		t.Errorf("request body contains reasoning text, want dropped: %s", fs.rawBody())
	}
}

func TestRequestShapeMaxTokensField(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	maxTokens := 123
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if _, ok := raw["max_completion_tokens"]; ok {
		t.Errorf("request has max_completion_tokens field, want only max_tokens: %s", fs.rawBody())
	}
	var got int
	if err := json.Unmarshal(raw["max_tokens"], &got); err != nil {
		t.Fatalf("decode max_tokens: %v", err)
	}
	if got != 123 {
		t.Errorf("max_tokens = %d, want 123", got)
	}
}

// TestRequestShapePenaltiesSeed asserts call.PresencePenalty and
// call.FrequencyPenalty serialize under their OpenAI-matching wire names,
// call.Seed serializes under Mistral's "random_seed" name, and call.TopK is
// NOT sent (Mistral's chat completions API has no top_k parameter).
func TestRequestShapePenaltiesSeed(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	presence := 0.5
	frequency := -0.5
	seed := int64(7)
	topK := 40
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:         []provider.Message{provider.UserText("simple")},
		PresencePenalty:  &presence,
		FrequencyPenalty: &frequency,
		Seed:             &seed,
		TopK:             &topK,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var gotPresence, gotFrequency float64
	var gotSeed int64
	if err := json.Unmarshal(raw["presence_penalty"], &gotPresence); err != nil || gotPresence != presence {
		t.Errorf("presence_penalty = %s, want %v", raw["presence_penalty"], presence)
	}
	if err := json.Unmarshal(raw["frequency_penalty"], &gotFrequency); err != nil || gotFrequency != frequency {
		t.Errorf("frequency_penalty = %s, want %v", raw["frequency_penalty"], frequency)
	}
	if err := json.Unmarshal(raw["random_seed"], &gotSeed); err != nil || gotSeed != seed {
		t.Errorf("random_seed = %s, want %v", raw["random_seed"], seed)
	}
	if _, ok := raw["seed"]; ok {
		t.Errorf("request has seed field, want only random_seed: %s", fs.rawBody())
	}
	if _, ok := raw["top_k"]; ok {
		t.Errorf("request unexpectedly contains top_k (unsupported by Mistral): %s", fs.rawBody())
	}
}

// TestRequestShapeHeaders asserts call.Headers entries are sent as extra
// HTTP headers, and that an entry matching the auth header name
// (case-insensitively) does not clobber the API key.
func TestRequestShapeHeaders(t *testing.T) {
	var gotCustom, gotAuth string
	hdrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom-Header")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		content := "hi"
		resp := chatResponse{Choices: []chatResponseChoice{{
			Message:      chatResponseMessage{Content: &content},
			FinishReason: "stop",
		}}}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(hdrSrv.Close)
	model := New(WithAPIKey("k"), WithBaseURL(hdrSrv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"authorization":   "should-not-win",
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotCustom != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want custom-value", gotCustom)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k (Headers must not clobber auth)", gotAuth)
	}
}

func TestRequestShapeToolChoiceRequiredIsAny(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceRequired},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var toolChoice string
	if err := json.Unmarshal(raw["tool_choice"], &toolChoice); err != nil {
		t.Fatalf("decode tool_choice: %v", err)
	}
	if toolChoice != "any" {
		t.Errorf("tool_choice = %q, want %q", toolChoice, "any")
	}
}

func TestRequestShapeToolChoiceModes(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	cases := []struct {
		mode provider.ToolChoiceMode
		want string
	}{
		{provider.ToolChoiceAuto, `"auto"`},
		{provider.ToolChoiceNone, `"none"`},
	}
	for _, tc := range cases {
		_, err := model.Generate(context.Background(), provider.Call{
			Messages:   []provider.Message{provider.UserText("simple")},
			ToolChoice: &provider.ToolChoice{Mode: tc.mode},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var raw map[string]json.RawMessage
		json.Unmarshal(fs.rawBody(), &raw)
		if tc.mode == provider.ToolChoiceNone {
			if _, ok := raw["tool_choice"]; ok {
				t.Errorf("mode %v: request has tool_choice field, want omitted", tc.mode)
			}
			continue
		}
		if string(raw["tool_choice"]) != tc.want {
			t.Errorf("mode %v: tool_choice = %s, want %s", tc.mode, raw["tool_choice"], tc.want)
		}
	}

	// tool mode
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var tc wireToolChoiceObj
	if err := json.Unmarshal(raw["tool_choice"], &tc); err != nil {
		t.Fatalf("decode tool_choice: %v", err)
	}
	if tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_choice = %+v, want {function, get_weather}", tc)
	}
}

func TestRequestShapeResponseFormatJSONObjectNoSchema(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var rf map[string]json.RawMessage
	if err := json.Unmarshal(raw["response_format"], &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	var typ string
	json.Unmarshal(rf["type"], &typ)
	if typ != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", typ)
	}
	if _, ok := rf["json_schema"]; ok {
		t.Errorf("response_format has json_schema field, want absent (Mistral has no schema mode): %s", raw["response_format"])
	}
	if _, ok := rf["schema"]; ok {
		t.Errorf("response_format has schema field, want absent: %s", raw["response_format"])
	}
}

func TestRequestShapeUnsupportedResponseFormatType(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "xml"},
	})
	if err == nil {
		t.Fatal("Generate: want error for unsupported ResponseFormat.Type, got nil")
	}
}

func TestRequestShapeToolMessageFilePartErrors(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.FilePart{
					Data:      []byte("data"),
					MediaType: "application/pdf",
				}},
			},
		},
	})
	if err == nil {
		t.Fatal("Generate: want error for FilePart in tool message, got nil")
	}
	if !strings.Contains(err.Error(), "FilePart") {
		t.Errorf("error = %q, want it to mention FilePart", err.Error())
	}
}

func TestRequestShapeAssistantMessageFilePartErrors(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{provider.FilePart{
					Data:      []byte("data"),
					MediaType: "application/pdf",
				}},
			},
		},
	})
	if err == nil {
		t.Fatal("Generate: want error for FilePart in assistant message, got nil")
	}
	if !strings.Contains(err.Error(), "FilePart") {
		t.Errorf("error = %q, want it to mention FilePart", err.Error())
	}
}

func TestRequestShapeToolResultOneMessagePerPart(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{
					provider.ToolResultPart{ToolCallID: "call_1", Name: "get_weather", Result: map[string]any{"temp": 42}},
					provider.ToolResultPart{ToolCallID: "call_2", Name: "get_time", Result: map[string]any{"time": "noon"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.request()
	if len(req.Messages) != 3 {
		t.Fatalf("Messages = %d, want 3 (user + one tool message per ToolResultPart)", len(req.Messages))
	}
	if req.Messages[1].Role != "tool" || req.Messages[1].ToolCallID != "call_1" || req.Messages[1].Name != "get_weather" {
		t.Errorf("Messages[1] = %+v, want tool/call_1/get_weather", req.Messages[1])
	}
	if req.Messages[2].Role != "tool" || req.Messages[2].ToolCallID != "call_2" || req.Messages[2].Name != "get_time" {
		t.Errorf("Messages[2] = %+v, want tool/call_2/get_time", req.Messages[2])
	}
}

// TestRequestShapeToolResultMultiModalProjectsToText verifies that a Tool
// result of type ai.ToolResultContent is projected down to its Text field
// for the "tool" message content — Mistral has no image slot in a tool
// result, so Images is silently dropped.
func TestRequestShapeToolResultMultiModalProjectsToText(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{
					provider.ToolResultPart{
						ToolCallID: "call_1",
						Name:       "chart",
						Result: ai.ToolResultContent{
							Text: "here's a chart",
							Images: []provider.GeneratedImage{
								{Data: []byte("fakepngbytes"), MediaType: "image/png"},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.request()
	if len(req.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(req.Messages))
	}
	// Content is double-JSON-encoded (matching the existing convention for
	// other Result values in this converter: json.Marshal(result), then
	// json.Marshal(that string)), so unmarshal twice to get the projected
	// text.
	var once string
	if err := json.Unmarshal(req.Messages[1].Content, &once); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal([]byte(once), &text); err != nil {
		t.Fatal(err)
	}
	if text != "here's a chart" {
		t.Errorf("projected tool result text = %q, want %q", text, "here's a chart")
	}
}

func TestGenerateToolResponseUsage(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("tool")},
		Tools:    []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Usage.TotalTokens != resp.Usage.InputTokens+resp.Usage.OutputTokens {
		t.Errorf("TotalTokens = %d, want sum of input+output", resp.Usage.TotalTokens)
	}
}

// rawSSEServer starts an httptest server that writes exactly the given raw
// SSE "data: ..." lines and then closes the response — simulating a
// proxy/load balancer that truncates the stream.
func rawSSEServer(t *testing.T, chunks []chatStreamChunk, sendDone bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			writeSSE(w, flusher, c)
		}
		if sendDone {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
		// Otherwise: deliberately no [DONE] — the handler returns here,
		// closing the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamEndsWithoutDoneButHasFinishReason covers the case where the
// server sends a chunk with finish_reason (and usage) but the connection
// closes before "data: [DONE]" arrives. This must still yield exactly one
// FinishPart with Err() == nil.
func TestStreamEndsWithoutDoneButHasFinishReason(t *testing.T) {
	stop := "stop"
	srv := rawSSEServer(t, []chatStreamChunk{
		{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}},
		{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "lo!"}}}},
		{Choices: []chatStreamChoice{{FinishReason: &stop}}, Usage: &wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
	}, false)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (finish_reason was received before truncation)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.TotalTokens != 7 {
		t.Errorf("FinishPart.Usage.TotalTokens = %d, want 7", finishes[0].Usage.TotalTokens)
	}
}

// TestStreamTruncatedBeforeFinishReason covers the case where the
// connection closes before any chunk with finish_reason arrives at all — a
// true mid-response truncation. This must yield zero FinishParts and a
// non-nil Err().
func TestStreamTruncatedBeforeFinishReason(t *testing.T) {
	srv := rawSSEServer(t, []chatStreamChunk{
		{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}},
	}, false)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err == nil {
		t.Fatal("Err() = nil, want non-nil (stream truncated before finish_reason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}

func TestStreamClosedTwiceIsIdempotent(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream simple")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range sr.Parts() {
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #1 = %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #2 = %v, want nil (idempotent)", err)
	}
}
