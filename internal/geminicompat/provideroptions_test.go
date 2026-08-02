package geminicompat

// Request-shape tests for ProviderOptions wiring: (a) an option key
// overriding an SDK-built field, (b) a novel passthrough key not otherwise
// exposed by this SDK. White-box (package geminicompat) since they decode
// the recorded raw request body directly.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestChatProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	temp := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:    []provider.Message{provider.UserText("simple")},
		Temperature: &temp,
		ProviderOptions: map[string]any{
			"google": map[string]any{
				"generationConfig": map[string]any{"temperature": 0.9},
				"safetySettings":   []any{map[string]any{"category": "HARM_CATEGORY_HARASSMENT"}},
			},
			"other-provider": map[string]any{
				"generationConfig": map[string]any{"temperature": 0.5},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var gc map[string]json.RawMessage
	if err := json.Unmarshal(raw["generationConfig"], &gc); err != nil {
		t.Fatalf("decode generationConfig: %v", err)
	}
	var temperature float64
	if err := json.Unmarshal(gc["temperature"], &temperature); err != nil {
		t.Fatalf("decode temperature: %v", err)
	}
	if temperature != 0.9 {
		t.Errorf("generationConfig.temperature = %v, want provider option override 0.9", temperature)
	}

	if _, ok := raw["safetySettings"]; !ok {
		t.Errorf("request missing novel top-level passthrough key safetySettings: %s", lastRawBody(srv))
	}
}

func TestImageProviderOptionsOverridesAndPassthrough(t *testing.T) {
	const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = body

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + onePixelPNGBase64 + `","mimeType":"image/png"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name: "google",
		EndpointFor: func(modelID, method string) string {
			return srv.URL + "/models/" + modelID + ":" + method
		},
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("x-goog-api-key", "k")
			return nil
		},
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      2,
		ProviderOptions: map[string]any{
			"google": map[string]any{
				"parameters": map[string]any{"sampleCount": 1},
				"instances":  []any{map[string]any{"prompt": "a dog"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw["parameters"], &params); err != nil {
		t.Fatalf("decode parameters: %v", err)
	}
	var sampleCount float64
	if err := json.Unmarshal(params["sampleCount"], &sampleCount); err != nil {
		t.Fatalf("decode sampleCount: %v", err)
	}
	if sampleCount != 1 {
		t.Errorf("parameters.sampleCount = %v, want provider option override 1", sampleCount)
	}

	var instances []map[string]string
	if err := json.Unmarshal(raw["instances"], &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) != 1 || instances[0]["prompt"] != "a dog" {
		t.Errorf("instances = %+v, want provider option override [{prompt: a dog}]", instances)
	}
}

func TestEmbeddingProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	cfg := testConfig(srv.URL)
	cfg.EmbedBatch = 100
	model := NewEmbeddingModel(cfg, "text-embedding-test")

	m2, ok := model.(provider.EmbeddingModelV2)
	if !ok {
		t.Fatalf("geminicompat embeddingModel does not implement provider.EmbeddingModelV2")
	}

	_, err := m2.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values: []string{"a"},
		ProviderOptions: map[string]any{
			"google": map[string]any{
				"title": "a novel top-level field",
			},
		},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	titleRaw, ok := raw["title"]
	if !ok {
		t.Fatalf("request missing novel passthrough key title: %s", reqs[0])
	}
	var title string
	if err := json.Unmarshal(titleRaw, &title); err != nil {
		t.Fatalf("decode title: %v", err)
	}
	if title != "a novel top-level field" {
		t.Errorf("title = %q, want %q", title, "a novel top-level field")
	}
}
