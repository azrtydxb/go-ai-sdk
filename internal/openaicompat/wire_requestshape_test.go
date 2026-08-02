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
	"encoding/json"
	"strings"
	"testing"

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
