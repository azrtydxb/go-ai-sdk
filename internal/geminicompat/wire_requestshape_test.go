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
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
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
		AuthHeaderName: "x-goog-api-key",
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

// TestRequestShapeToolResultMultiModalProjectsToText verifies that a Tool
// result of type ai.ToolResultContent is projected down to its Text field
// for the functionResponse Output — Gemini's functionResponse has no image
// slot in a tool result, so Images is silently dropped.
func TestRequestShapeToolResultMultiModalProjectsToText(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.ToolResultPart{
					ToolCallID: "call_1",
					Name:       "chart",
					Result: ai.ToolResultContent{
						Text: "here's a chart",
						Images: []provider.GeneratedImage{
							{Data: []byte("fakepngbytes"), MediaType: "image/png"},
						},
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	fr := req.Contents[1].Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("no functionResponse part found")
	}
	text, ok := fr.Response.Output.(string)
	if !ok {
		t.Fatalf("FunctionResponse.Response.Output = %#v, want plain string (text-only projection)", fr.Response.Output)
	}
	if text != "here's a chart" {
		t.Errorf("projected tool result text = %q, want %q", text, "here's a chart")
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

func TestRequestShapeUserMessageFilePartPDF(t *testing.T) {
	model, srv := newTestLanguageModel(t)

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

	req := lastRequest(t, srv)
	if len(req.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1", len(req.Contents))
	}
	parts := req.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("Parts = %d, want 2", len(parts))
	}
	inline := parts[1].InlineData
	if inline == nil {
		t.Fatalf("Parts[1].InlineData = nil, want set")
	}
	if inline.MimeType != "application/pdf" {
		t.Errorf("InlineData.MimeType = %q, want application/pdf", inline.MimeType)
	}
	wantData := base64.StdEncoding.EncodeToString(pdfData)
	if inline.Data != wantData {
		t.Errorf("InlineData.Data = %q, want %q", inline.Data, wantData)
	}
}

func TestRequestShapeUserMessageFilePartAudio(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	audioData := []byte("fake mp3 bytes")
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "simple"},
					provider.FilePart{Data: audioData, MediaType: "audio/mpeg"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	parts := req.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("Parts = %d, want 2", len(parts))
	}
	inline := parts[1].InlineData
	if inline == nil {
		t.Fatalf("Parts[1].InlineData = nil, want set")
	}
	if inline.MimeType != "audio/mpeg" {
		t.Errorf("InlineData.MimeType = %q, want audio/mpeg", inline.MimeType)
	}
	wantData := base64.StdEncoding.EncodeToString(audioData)
	if inline.Data != wantData {
		t.Errorf("InlineData.Data = %q, want %q", inline.Data, wantData)
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

// TestRequestShapeTopKPenaltiesSeed asserts call.TopK, call.PresencePenalty,
// call.FrequencyPenalty, and call.Seed serialize into generationConfig
// under their Gemini wire names.
func TestRequestShapeTopKPenaltiesSeed(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	topK := 40
	presence := 0.3
	frequency := -0.2
	seed := int64(99)
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:         []provider.Message{provider.UserText("simple")},
		TopK:             &topK,
		PresencePenalty:  &presence,
		FrequencyPenalty: &frequency,
		Seed:             &seed,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gc := lastRequest(t, srv).GenerationConfig
	if gc == nil {
		t.Fatalf("GenerationConfig is nil, want set")
	}
	if gc.TopK == nil || *gc.TopK != 40 {
		t.Errorf("TopK = %v, want 40", gc.TopK)
	}
	if gc.PresencePenalty == nil || *gc.PresencePenalty != 0.3 {
		t.Errorf("PresencePenalty = %v, want 0.3", gc.PresencePenalty)
	}
	if gc.FrequencyPenalty == nil || *gc.FrequencyPenalty != -0.2 {
		t.Errorf("FrequencyPenalty = %v, want -0.2", gc.FrequencyPenalty)
	}
	if gc.Seed == nil || *gc.Seed != 99 {
		t.Errorf("Seed = %v, want 99", gc.Seed)
	}
}

// TestRequestShapeHeaders asserts call.Headers entries are sent as extra
// HTTP headers, and that an entry matching the auth header name
// (case-insensitively — here "x-goog-api-key", per testConfig's Authorize)
// does not clobber the API key.
func TestRequestShapeHeaders(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"X-Goog-Api-Key":  "should-not-win",
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got := srv.HeaderValues("X-Custom-Header"); len(got) != 1 || got[0] != "custom-value" {
		t.Errorf("X-Custom-Header = %v, want [custom-value]", got)
	}
	if got := srv.HeaderValues("x-goog-api-key"); len(got) != 1 || got[0] != "k" {
		t.Errorf("x-goog-api-key = %v, want [k] (Headers must not clobber auth)", got)
	}
}
