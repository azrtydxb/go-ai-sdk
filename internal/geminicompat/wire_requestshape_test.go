package geminicompat

// Request-shape tests: these assert on the raw JSON produced by
// buildGenerateContentRequest for various provider.Call inputs. They are
// white-box (package geminicompat) because they inspect geminicompat's
// unexported wire types directly — this is purely about wire serialization,
// so it lives here rather than in providers/google (moved verbatim from the
// google package's former google_test.go, only the model construction
// changed from google.New(...).Model(...) to
// NewLanguageModel(Config{...}, ...)).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func testConfig(baseURL string) Config {
	return Config{
		Name: "google",
		EndpointFor: func(modelID, method string) string {
			return baseURL + "/models/" + modelID + ":" + method
		},
		Authorize: func(_ context.Context, req *http.Request) error {
			req.Header.Set("x-goog-api-key", "k")
			return nil
		},
	}
}

func newTestLanguageModel(t *testing.T) (provider.LanguageModel, *compattest.Server) {
	t.Helper()
	srv := compattest.NewFixtureServer(t, "google")
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")
	return model, srv
}

func lastRawBody(srv *compattest.Server) []byte {
	reqs := srv.Requests()
	if len(reqs) == 0 {
		return nil
	}
	return reqs[len(reqs)-1]
}

func lastRequest(t *testing.T, srv *compattest.Server) generateContentRequest {
	t.Helper()
	var req generateContentRequest
	if err := json.Unmarshal(lastRawBody(srv), &req); err != nil {
		t.Fatalf("decode last request: %v", err)
	}
	return req
}

func TestRequestShapeSystemExtraction(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.SystemText("You are helpful."),
			provider.SystemText("Be concise."),
			provider.UserText("simple"),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	if req.SystemInstruction == nil {
		t.Fatalf("SystemInstruction is nil, want set")
	}
	if len(req.SystemInstruction.Parts) != 1 {
		t.Fatalf("SystemInstruction.Parts = %d, want 1", len(req.SystemInstruction.Parts))
	}
	if want := "You are helpful.\n\nBe concise."; req.SystemInstruction.Parts[0].Text != want {
		t.Errorf("SystemInstruction text = %q, want %q", req.SystemInstruction.Parts[0].Text, want)
	}
	if len(req.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1 (system messages must not appear in contents[])", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Errorf("Contents[0].Role = %q, want user", req.Contents[0].Role)
	}
}

func TestRequestShapeAdditionalPropertiesStripped(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"city":{"type":"string"},
			"nested":{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":false}
		},
		"additionalProperties":false
	}`)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get the weather",
			Schema:      schema,
		}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	raw := lastRawBody(srv)
	if strings.Contains(string(raw), "additionalProperties") {
		t.Errorf("raw request contains additionalProperties (must be stripped recursively): %s", raw)
	}

	req := lastRequest(t, srv)
	if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("Tools shape unexpected: %+v", req.Tools)
	}
	decl := req.Tools[0].FunctionDeclarations[0]
	if decl.Name != "get_weather" {
		t.Errorf("declaration name = %q, want get_weather", decl.Name)
	}

	var params map[string]any
	if err := json.Unmarshal(decl.Parameters, &params); err != nil {
		t.Fatalf("decode parameters: %v", err)
	}
	assertNoAdditionalProperties(t, params)
}

// assertNoAdditionalProperties recursively verifies that no map in v
// contains an "additionalProperties" key.
func assertNoAdditionalProperties(t *testing.T, v any) {
	t.Helper()
	switch t2 := v.(type) {
	case map[string]any:
		if _, ok := t2["additionalProperties"]; ok {
			t.Errorf("found additionalProperties in schema: %v", t2)
		}
		for _, val := range t2 {
			assertNoAdditionalProperties(t, val)
		}
	case []any:
		for _, val := range t2 {
			assertNoAdditionalProperties(t, val)
		}
	}
}

func TestRequestShapeToolConfigModes(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	cases := []struct {
		mode provider.ToolChoiceMode
		want string
	}{
		{provider.ToolChoiceAuto, "AUTO"},
		{provider.ToolChoiceNone, "NONE"},
		{provider.ToolChoiceRequired, "ANY"},
	}
	for _, tc := range cases {
		_, err := model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("simple")},
			Tools: []provider.ToolDef{{
				Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`),
			}},
			ToolChoice: &provider.ToolChoice{Mode: tc.mode},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		req := lastRequest(t, srv)
		if req.ToolConfig == nil {
			t.Fatalf("mode %v: ToolConfig is nil", tc.mode)
		}
		if got := req.ToolConfig.FunctionCallingConfig.Mode; got != tc.want {
			t.Errorf("mode %v: functionCallingConfig.mode = %q, want %q", tc.mode, got, tc.want)
		}
	}

	// specific tool -> ANY + allowedFunctionNames
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	fcc := lastRequest(t, srv).ToolConfig.FunctionCallingConfig
	if fcc.Mode != "ANY" {
		t.Errorf("specific tool: mode = %q, want ANY", fcc.Mode)
	}
	if len(fcc.AllowedFunctionNames) != 1 || fcc.AllowedFunctionNames[0] != "get_weather" {
		t.Errorf("allowedFunctionNames = %v, want [get_weather]", fcc.AllowedFunctionNames)
	}
}

func TestRequestShapeResponseFormatJSON(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":false}`)
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "json", Schema: schema},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	if req.GenerationConfig == nil {
		t.Fatalf("GenerationConfig is nil, want set")
	}
	if req.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("responseMimeType = %q, want application/json", req.GenerationConfig.ResponseMimeType)
	}
	if len(req.GenerationConfig.ResponseSchema) == 0 {
		t.Fatalf("responseSchema is empty, want the (stripped) schema")
	}
	var schemaVal map[string]any
	if err := json.Unmarshal(req.GenerationConfig.ResponseSchema, &schemaVal); err != nil {
		t.Fatalf("decode responseSchema: %v", err)
	}
	assertNoAdditionalProperties(t, schemaVal)
}

func TestRequestShapeGenerationConfig(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	maxTokens := 123
	temp := 0.5
	topP := 0.9
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:      []provider.Message{provider.UserText("simple")},
		MaxTokens:     &maxTokens,
		Temperature:   &temp,
		TopP:          &topP,
		StopSequences: []string{"STOP"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gc := lastRequest(t, srv).GenerationConfig
	if gc == nil {
		t.Fatalf("GenerationConfig is nil, want set")
	}
	if gc.MaxOutputTokens == nil || *gc.MaxOutputTokens != 123 {
		t.Errorf("MaxOutputTokens = %v, want 123", gc.MaxOutputTokens)
	}
	if gc.Temperature == nil || *gc.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", gc.Temperature)
	}
	if gc.TopP == nil || *gc.TopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", gc.TopP)
	}
	if len(gc.StopSequences) != 1 || gc.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %v, want [STOP]", gc.StopSequences)
	}
}

func TestRequestShapeToolResultFunctionResponse(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.ToolResultPart{
					ToolCallID: "call_1",
					Name:       "get_weather",
					Result:     map[string]any{"temp": 42},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	if len(req.Contents) != 2 {
		t.Fatalf("Contents = %d, want 2", len(req.Contents))
	}
	toolContent := req.Contents[1]
	if toolContent.Role != "user" {
		t.Errorf("tool result content Role = %q, want user", toolContent.Role)
	}
	if len(toolContent.Parts) != 1 || toolContent.Parts[0].FunctionResponse == nil {
		t.Fatalf("tool result content Parts = %+v, want 1 functionResponse part", toolContent.Parts)
	}
	fr := toolContent.Parts[0].FunctionResponse
	if fr.Name != "get_weather" {
		t.Errorf("FunctionResponse.Name = %q, want get_weather", fr.Name)
	}
	m, ok := fr.Response.Output.(map[string]any)
	if !ok {
		t.Fatalf("FunctionResponse.Response.Output = %#v, want map", fr.Response.Output)
	}
	if m["temp"] != float64(42) {
		t.Errorf("output.temp = %v, want 42", m["temp"])
	}
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

func TestRequestShapeAssistantToolCallParsedArgs(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{provider.ToolCallPart{
					ID:   "call_1",
					Name: "get_weather",
					Args: json.RawMessage(`{"city":"Ghent"}`),
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	content := req.Contents[1]
	if content.Role != "model" {
		t.Errorf("assistant content Role = %q, want model", content.Role)
	}
	part := content.Parts[0]
	if part.FunctionCall == nil {
		t.Fatalf("Parts[0].FunctionCall is nil")
	}
	var args map[string]string
	if err := json.Unmarshal(part.FunctionCall.Args, &args); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if args["city"] != "Ghent" {
		t.Errorf("args.city = %q, want Ghent", args["city"])
	}
}
