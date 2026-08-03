package openaicompat

// Request-shape tests: these assert on the raw JSON produced by
// buildChatRequest for various provider.Call inputs. They are white-box
// (package openaicompat) because they inspect openaicompat's unexported wire
// types directly — this is purely about wire serialization, so it lives
// here rather than in providers/openai (moved verbatim from the openai
// package's former openai_test.go, only the model construction changed from
// openai.New(...).Model(...) to NewLanguageModel(Config{...}, ...)).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func newTestLanguageModel(t *testing.T) (provider.LanguageModel, *compattest.Server) {
	t.Helper()
	srv := compattest.NewFixtureServer(t, "openai")
	model := NewLanguageModel(Config{Name: "openai", APIKey: "k", BaseURL: srv.URL, NativeJSON: true}, "gpt-test")
	return model, srv
}

func lastRawBody(srv *compattest.Server) []byte {
	reqs := srv.Requests()
	if len(reqs) == 0 {
		return nil
	}
	return reqs[len(reqs)-1]
}

func TestRequestShapeToolMessageFilePartErrors(t *testing.T) {
	model, _ := newTestLanguageModel(t)

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

// TestRequestShapeToolResultMultiModalProjectsToText verifies that a Tool
// result of type ai.ToolResultContent is projected down to its Text field
// for the "tool" message content — OpenAI's wire format has no image slot
// in a tool result, so Images is silently dropped.
func TestRequestShapeToolResultMultiModalProjectsToText(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{Role: provider.RoleAssistant, Content: []provider.ContentPart{
				provider.ToolCallPart{ID: "c1", Name: "chart", Args: json.RawMessage(`{}`)},
			}},
			{Role: provider.RoleTool, Content: []provider.ContentPart{
				provider.ToolResultPart{
					ToolCallID: "c1",
					Name:       "chart",
					Result: ai.ToolResultContent{
						Text: "here's a chart",
						Images: []provider.GeneratedImage{
							{Data: []byte("fakepngbytes"), MediaType: "image/png"},
						},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(raw["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	toolMsg := msgs[len(msgs)-1]
	var role string
	json.Unmarshal(toolMsg["role"], &role)
	if role != "tool" {
		t.Fatalf("last message role = %q, want tool", role)
	}
	var content string
	if err := json.Unmarshal(toolMsg["content"], &content); err != nil {
		t.Fatalf("tool message content is not a plain string (want text-only projection): %s", toolMsg["content"])
	}
	// content is itself a JSON-encoded string (matching the existing
	// double-encoding convention for other Result values in this
	// converter), so unmarshal once more to get the projected text.
	var innerText string
	if err := json.Unmarshal([]byte(content), &innerText); err != nil {
		t.Fatal(err)
	}
	if innerText != "here's a chart" {
		t.Errorf("projected tool result text = %q, want %q", innerText, "here's a chart")
	}
	if strings.Contains(string(lastRawBody(srv)), "fakepngbytes") {
		t.Error("raw request body contains image bytes; Images should be dropped for this provider")
	}
}

func TestRequestShapeAssistantMessageFilePartErrors(t *testing.T) {
	model, _ := newTestLanguageModel(t)

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

func TestRequestShapeUserMessageFilePartPDF(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	pdfData := []byte("%PDF-1.4 fake pdf bytes")
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "also text"},
					provider.FilePart{Data: pdfData, MediaType: "application/pdf", Filename: "report.pdf"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var req chatRequest
	if err := json.Unmarshal(lastRawBody(srv), &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(req.Messages))
	}
	var parts []wireContentPart
	if err := json.Unmarshal(req.Messages[1].Content, &parts); err != nil {
		t.Fatalf("decode content parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	filePart := parts[1]
	if filePart.Type != "file" {
		t.Errorf("parts[1].Type = %q, want file", filePart.Type)
	}
	if filePart.File == nil {
		t.Fatalf("parts[1].File = nil, want set")
	}
	if filePart.File.Filename != "report.pdf" {
		t.Errorf("File.Filename = %q, want report.pdf", filePart.File.Filename)
	}
	wantDataURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdfData)
	if filePart.File.FileData != wantDataURL {
		t.Errorf("File.FileData = %q, want %q", filePart.File.FileData, wantDataURL)
	}
}

func TestRequestShapeUserMessageFilePartDefaultFilename(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "also text"},
					provider.FilePart{Data: []byte("pdf bytes"), MediaType: "application/pdf"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var req chatRequest
	if err := json.Unmarshal(lastRawBody(srv), &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var parts []wireContentPart
	if err := json.Unmarshal(req.Messages[1].Content, &parts); err != nil {
		t.Fatalf("decode content parts: %v", err)
	}
	if len(parts) != 2 || parts[1].File == nil {
		t.Fatalf("parts = %+v, want 2 parts with file set", parts)
	}
	if parts[1].File.Filename != "file.pdf" {
		t.Errorf("File.Filename = %q, want default file.pdf", parts[1].File.Filename)
	}
}

// TestRequestShapeUserMessageFilePartMediaTypeMatchingIsLenient verifies PDF
// media-type matching ignores case and strips MIME parameters (via
// mime.ParseMediaType), per RFC 2045, rather than requiring an exact
// "application/pdf" string match.
func TestRequestShapeUserMessageFilePartMediaTypeMatchingIsLenient(t *testing.T) {
	for _, mediaType := range []string{"Application/PDF", "application/pdf; name=x"} {
		t.Run(mediaType, func(t *testing.T) {
			model, srv := newTestLanguageModel(t)

			_, err := model.Generate(context.Background(), provider.Call{
				Messages: []provider.Message{
					provider.UserText("simple"),
					{
						Role: provider.RoleUser,
						Content: []provider.ContentPart{
							provider.TextPart{Text: "also text"},
							provider.FilePart{Data: []byte("data"), MediaType: mediaType},
						},
					},
				},
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			var req chatRequest
			if err := json.Unmarshal(lastRawBody(srv), &req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			var parts []wireContentPart
			if err := json.Unmarshal(req.Messages[1].Content, &parts); err != nil {
				t.Fatalf("decode content parts: %v", err)
			}
			if len(parts) != 2 || parts[1].Type != "file" || parts[1].File == nil {
				t.Fatalf("parts = %+v, want a text part followed by a file part", parts)
			}
		})
	}
}

func TestRequestShapeUserMessageFilePartNonPDFErrors(t *testing.T) {
	model, _ := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{provider.FilePart{
					Data:      []byte("data"),
					MediaType: "audio/mpeg",
				}},
			},
		},
	})
	if err == nil {
		t.Fatal("Generate: want error for non-PDF FilePart in user message, got nil")
	}
	if !strings.Contains(err.Error(), "FilePart") {
		t.Errorf("error = %q, want it to mention FilePart", err.Error())
	}
}

func TestRequestShapeTools(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	maxTokens := 42
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get the weather",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object"}`),
		},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	if _, ok := raw["tools"]; !ok {
		t.Errorf("request missing tools field: %s", lastRawBody(srv))
	}
	var tools []wireTool
	json.Unmarshal(raw["tools"], &tools)
	if len(tools) != 1 || tools[0].Type != "function" || tools[0].Function.Name != "get_weather" {
		t.Errorf("tools = %+v, want one function tool named get_weather", tools)
	}

	var toolChoice wireToolChoiceObj
	if err := json.Unmarshal(raw["tool_choice"], &toolChoice); err != nil {
		t.Fatalf("decode tool_choice: %v", err)
	}
	if toolChoice.Type != "function" || toolChoice.Function.Name != "get_weather" {
		t.Errorf("tool_choice = %+v, want {function, get_weather}", toolChoice)
	}

	var rf wireJSONSchemaFormat
	if err := json.Unmarshal(raw["response_format"], &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if rf.Type != "json_schema" || rf.JSONSchema == nil || rf.JSONSchema.Name != "weather_schema" || !rf.JSONSchema.Strict {
		t.Errorf("response_format = %+v, want json_schema/weather_schema/strict", rf)
	}

	var mct int
	if err := json.Unmarshal(raw["max_completion_tokens"], &mct); err != nil {
		t.Fatalf("decode max_completion_tokens: %v", err)
	}
	if mct != 42 {
		t.Errorf("max_completion_tokens = %d, want 42", mct)
	}
}

func TestRequestShapeToolChoiceModes(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	cases := []struct {
		mode provider.ToolChoiceMode
		want string
	}{
		{provider.ToolChoiceAuto, `"auto"`},
		{provider.ToolChoiceNone, `"none"`},
		{provider.ToolChoiceRequired, `"required"`},
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
		json.Unmarshal(lastRawBody(srv), &raw)
		if string(raw["tool_choice"]) != tc.want {
			t.Errorf("mode %v: tool_choice = %s, want %s", tc.mode, raw["tool_choice"], tc.want)
		}
	}
}

func TestRequestShapeJSONObjectResponseFormat(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "json"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(lastRawBody(srv), &raw)
	var rf map[string]string
	json.Unmarshal(raw["response_format"], &rf)
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", rf["type"])
	}
}

func TestRequestShapeUnsupportedResponseFormatType(t *testing.T) {
	model, _ := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "xml"},
	})
	if err == nil {
		t.Fatal("Generate: want error for unsupported ResponseFormat.Type, got nil")
	}
}

// TestRequestShapeMaxTokensParam asserts that Config.MaxTokensParam selects
// the wire field name used to send call.MaxTokens: empty defaults to
// "max_completion_tokens" (OpenAI's current name), while providers that
// still document "max_tokens" (Perplexity, Fireworks, Together, DeepSeek)
// set it explicitly.
func TestRequestShapeMaxTokensParam(t *testing.T) {
	cases := []struct {
		name           string
		maxTokensParam string
		wantField      string
	}{
		{"default (unset)", "", "max_completion_tokens"},
		{"explicit max_completion_tokens", "max_completion_tokens", "max_completion_tokens"},
		{"explicit max_tokens", "max_tokens", "max_tokens"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := compattest.NewFixtureServer(t, "test")
			defer srv.Close()
			model := NewLanguageModel(Config{
				Name:           "test",
				APIKey:         "k",
				BaseURL:        srv.URL,
				MaxTokensParam: tc.maxTokensParam,
			}, "test-model")

			maxTokens := 7
			_, err := model.Generate(context.Background(), provider.Call{
				Messages:  []provider.Message{provider.UserText("simple")},
				MaxTokens: &maxTokens,
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
				t.Fatalf("decode raw request: %v", err)
			}

			got, ok := raw[tc.wantField]
			if !ok {
				t.Fatalf("request missing %q field: %s", tc.wantField, lastRawBody(srv))
			}
			var n int
			if err := json.Unmarshal(got, &n); err != nil || n != 7 {
				t.Errorf("%s = %s, want 7", tc.wantField, got)
			}

			// The non-selected field name must not appear at all.
			otherField := "max_tokens"
			if tc.wantField == "max_tokens" {
				otherField = "max_completion_tokens"
			}
			if _, ok := raw[otherField]; ok {
				t.Errorf("request unexpectedly contains %q: %s", otherField, lastRawBody(srv))
			}
		})
	}
}

// TestRequestShapeAPIKeyHeader asserts that Config.APIKeyHeader selects the
// HTTP header used to carry the API key: empty (default) sends
// "Authorization: Bearer <key>", non-empty sends "<APIKeyHeader>: <key>"
// (no Bearer prefix) and no Authorization header at all.
func TestRequestShapeAPIKeyHeader(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	defer srv.Close()
	model := NewLanguageModel(Config{
		Name:         "test",
		APIKey:       "k",
		BaseURL:      srv.URL,
		APIKeyHeader: "api-key",
	}, "test-model")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got := srv.HeaderValues("api-key"); len(got) != 1 || got[0] != "k" {
		t.Errorf("api-key header = %v, want [k]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization header = %v, want [\"\"]", got)
	}
}

// TestRequestShapeJSONObjectOnly asserts that Config.JSONObjectOnly forces
// response_format to {"type":"json_object"} even when a Schema is supplied,
// dropping json_schema/schema entirely (DeepSeek rejects json_schema).
func TestRequestShapeJSONObjectOnly(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	defer srv.Close()
	model := NewLanguageModel(Config{
		Name:           "test",
		APIKey:         "k",
		BaseURL:        srv.URL,
		JSONObjectOnly: true,
	}, "test-model")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object"}`),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	rfRaw, ok := raw["response_format"]
	if !ok {
		t.Fatalf("request missing response_format field: %s", lastRawBody(srv))
	}
	if strings.Contains(string(rfRaw), "json_schema") || strings.Contains(string(rfRaw), "schema") {
		t.Errorf("response_format = %s, want no json_schema/schema keys", rfRaw)
	}

	var rf map[string]string
	if err := json.Unmarshal(rfRaw, &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", rf["type"])
	}
}

// TestRequestShapePresenceFrequencyPenaltySeed asserts call.PresencePenalty,
// call.FrequencyPenalty, and call.Seed serialize under their OpenAI wire
// names, and that call.TopK is NOT sent (OpenAI's chat-completions API has
// no top_k parameter).
func TestRequestShapePresenceFrequencyPenaltySeed(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	presence := 0.5
	frequency := -0.25
	seed := int64(42)
	topK := 7
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
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
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
	if err := json.Unmarshal(raw["seed"], &gotSeed); err != nil || gotSeed != seed {
		t.Errorf("seed = %s, want %v", raw["seed"], seed)
	}
	if _, ok := raw["top_k"]; ok {
		t.Errorf("request unexpectedly contains top_k (unsupported by OpenAI chat-completions): %s", lastRawBody(srv))
	}
}

// TestRequestShapeHeaders asserts call.Headers entries are sent as extra
// HTTP headers, and that an entry matching the auth header name
// (case-insensitively) does not clobber the API key.
func TestRequestShapeHeaders(t *testing.T) {
	model, srv := newTestLanguageModel(t)

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

	if got := srv.HeaderValues("X-Custom-Header"); len(got) != 1 || got[0] != "custom-value" {
		t.Errorf("X-Custom-Header = %v, want [custom-value]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "Bearer k" {
		t.Errorf("Authorization = %v, want [Bearer k] (Headers must not clobber auth)", got)
	}
}

// TestRequestShapeReasoningEffort asserts call.Reasoning.Effort serializes
// under "reasoning_effort", and that BudgetTokens (with no OpenAI-wire
// equivalent) is never sent.
func TestRequestShapeReasoningEffort(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	budget := 4096
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{Effort: "high", BudgetTokens: &budget},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var effort string
	if err := json.Unmarshal(raw["reasoning_effort"], &effort); err != nil || effort != "high" {
		t.Errorf("reasoning_effort = %s, want %q", raw["reasoning_effort"], "high")
	}
	if _, ok := raw["budget_tokens"]; ok {
		t.Errorf("request unexpectedly contains budget_tokens (no OpenAI-wire equivalent): %s", lastRawBody(srv))
	}
}

// TestRequestShapeReasoningOmittedWhenEffortEmpty asserts that Reasoning set
// with an empty Effort (and no wire equivalent for BudgetTokens) omits
// reasoning_effort entirely.
func TestRequestShapeReasoningOmittedWhenEffortEmpty(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if _, ok := raw["reasoning_effort"]; ok {
		t.Errorf("request unexpectedly contains reasoning_effort: %s", lastRawBody(srv))
	}
}
