package google

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

	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

// fixtureState records the last request the fixture server saw, so tests
// can assert on wire-level request shape.
type fixtureState struct {
	mu          sync.Mutex
	lastRawBody []byte
	lastRequest generateContentRequest
}

func (fs *fixtureState) record(raw []byte, req generateContentRequest) {
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

func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
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

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got == "" {
			t.Errorf("fixture: missing x-goog-api-key header")
		}

		switch {
		case strings.HasSuffix(r.URL.Path, ":batchEmbedContents"):
			handleEmbed(t, w, r)
			return
		case strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
			handleGenerate(t, w, r, fs, true)
			return
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			handleGenerate(t, w, r, fs, false)
			return
		default:
			t.Fatalf("fixture: unexpected path %q", r.URL.Path)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv, fs
}

func handleGenerate(t *testing.T, w http.ResponseWriter, r *http.Request, fs *fixtureState, stream bool) {
	t.Helper()

	if stream != (r.URL.Query().Get("alt") == "sse") {
		t.Errorf("fixture: alt=sse query param mismatch for path %q (stream=%v)", r.URL.Path, stream)
	}

	var req generateContentRequest
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
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		switch text {
		case "stream simple":
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content: wireContent{Role: "model", Parts: []wirePart{{Text: "Hel"}}},
				}},
			})
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content:      wireContent{Role: "model", Parts: []wirePart{{Text: "lo!"}}},
					FinishReason: "STOP",
				}},
				UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5},
			})
		case "stream tool":
			writeSSE(w, flusher, generateContentResponse{
				Candidates: []wireCandidate{{
					Content: wireContent{Role: "model", Parts: []wirePart{{
						FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
					}}},
					FinishReason: "STOP",
				}},
				UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
			})
		default:
			t.Fatalf("fixture: unknown streaming scenario %q", text)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch text {
	case "simple":
		resp := generateContentResponse{
			Candidates: []wireCandidate{{
				Content:      wireContent{Role: "model", Parts: []wirePart{{Text: "Hello from google!"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 5, CandidatesTokenCount: 3, TotalTokenCount: 8},
		}
		json.NewEncoder(w).Encode(resp)
	case "tool":
		resp := generateContentResponse{
			Candidates: []wireCandidate{{
				Content: wireContent{Role: "model", Parts: []wirePart{{
					FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
				}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
		}
		json.NewEncoder(w).Encode(resp)
	default:
		t.Fatalf("fixture: unknown scenario %q", text)
	}
}

func handleEmbed(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	var req batchEmbedRequest
	raw := readBody(t, r)
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("fixture: decode embed request: %v", err)
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
	json.NewEncoder(w).Encode(batchEmbedResponse{Embeddings: embeddings})
}

func TestConformance(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("gemini-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "google"})
}

func TestCapabilities(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")
	if caps := model.Capabilities(); !caps.NativeJSON {
		t.Errorf("Capabilities().NativeJSON = false, want true")
	}
}

func TestRequestShapeSystemExtraction(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

	req := fs.lastRequest
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
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

	raw := fs.rawBody()
	if strings.Contains(string(raw), "additionalProperties") {
		t.Errorf("raw request contains additionalProperties (must be stripped recursively): %s", raw)
	}

	if len(fs.lastRequest.Tools) != 1 || len(fs.lastRequest.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("Tools shape unexpected: %+v", fs.lastRequest.Tools)
	}
	decl := fs.lastRequest.Tools[0].FunctionDeclarations[0]
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
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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
		req := fs.lastRequest
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
	fcc := fs.lastRequest.ToolConfig.FunctionCallingConfig
	if fcc.Mode != "ANY" {
		t.Errorf("specific tool: mode = %q, want ANY", fcc.Mode)
	}
	if len(fcc.AllowedFunctionNames) != 1 || fcc.AllowedFunctionNames[0] != "get_weather" {
		t.Errorf("allowedFunctionNames = %v, want [get_weather]", fcc.AllowedFunctionNames)
	}
}

func TestRequestShapeResponseFormatJSON(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":false}`)
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "json", Schema: schema},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.lastRequest
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
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

	gc := fs.lastRequest.GenerationConfig
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
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

	req := fs.lastRequest
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

func TestRequestShapeAssistantToolCallParsedArgs(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

	req := fs.lastRequest
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

func TestGenerateToolResponseUsage(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

// streamSSEServer starts an httptest server that writes exactly the given
// raw response chunks as SSE "data:" events and then closes the response —
// simulating a proxy/load balancer that truncates the stream.
func streamSSEServer(t *testing.T, chunks []generateContentResponse) *httptest.Server {
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
		// Deliberately no closing marker — the handler returns here, closing
		// the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamToolCallIDsAreDistinctAcrossChunks covers two calls to the SAME
// tool name arriving in two SEPARATE SSE chunks. The synthesized ID must be
// unique per call (a counter across the whole stream), not reset to 0 for
// each chunk's parts slice — otherwise both calls would collide on
// "call_get_weather_0", breaking ID-based tool-call/result correlation.
func TestStreamToolCallIDsAreDistinctAcrossChunks(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{
			FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
		}}}}}},
		{
			Candidates: []wireCandidate{{
				Content: wireContent{Parts: []wirePart{{
					FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Bruges"}`)},
				}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
		},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
		Tools:    []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var ends []provider.ToolCallEnd
	for part := range sr.Parts() {
		if end, ok := part.(provider.ToolCallEnd); ok {
			ends = append(ends, end)
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(ends) != 2 {
		t.Fatalf("got %d ToolCallEnd part(s), want exactly 2: %+v", len(ends), ends)
	}
	if ends[0].Call.ID == ends[1].Call.ID {
		t.Errorf("ToolCallEnd IDs collide: both %q (must be distinct across chunks)", ends[0].Call.ID)
	}
}

// TestStreamEndsWithFinishReasonSeen covers the case where the connection
// closes right after a chunk carrying a finishReason (Gemini has no
// [DONE]/message_stop sentinel — natural EOF is the only signal). This must
// still yield exactly one FinishPart with Err() == nil.
func TestStreamEndsWithFinishReasonSeen(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "Hel"}}}}}},
		{
			Candidates:    []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "lo!"}}}, FinishReason: "STOP"}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5},
		},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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
		t.Fatalf("Err() = %v, want nil (finishReason was received)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.TotalTokens != 5 {
		t.Errorf("FinishPart.Usage.TotalTokens = %d, want 5", finishes[0].Usage.TotalTokens)
	}
}

// TestStreamTruncatedBeforeFinishReason covers a true mid-response
// truncation: the connection closes before any chunk carries a
// finishReason. This must yield zero FinishParts and a non-nil Err().
func TestStreamTruncatedBeforeFinishReason(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "Hel"}}}}}},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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
		t.Fatal("Err() = nil, want non-nil (stream truncated before finishReason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}

func TestStreamClosedTwiceIsIdempotent(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

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

func TestStreamPartsIsSingleUse(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream simple")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var first, second int
	for range sr.Parts() {
		first++
	}
	for range sr.Parts() {
		second++
	}
	if first == 0 {
		t.Fatalf("first Parts() iteration yielded 0 parts")
	}
	if second != 0 {
		t.Errorf("second Parts() iteration yielded %d parts, want 0 (single-use)", second)
	}
}

func TestGenerateContextCancellation(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := model.Generate(ctx, provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
	}
}
