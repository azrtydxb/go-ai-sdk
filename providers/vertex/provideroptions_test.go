package vertex

// Request-shape test for ProviderOptions wiring on the embedding model
// (Vertex's :predict endpoint): (a) an option key overriding an SDK-built
// field, (b) a novel passthrough key not otherwise exposed by this SDK.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestEmbeddingProviderOptionsOverridesAndPassthrough(t *testing.T) {
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"embeddings":{"values":[0.1,0.2],"statistics":{"token_count":1}}}]}`))
	}))
	t.Cleanup(srv.Close)

	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL(srv.URL),
		WithAccessToken("tok"),
	)
	model := p.EmbeddingModel("text-embedding-test")

	m2, ok := model.(provider.EmbeddingModelWithOptions)
	if !ok {
		t.Fatalf("vertex embeddingModel does not implement provider.EmbeddingModelWithOptions")
	}

	_, err := m2.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values: []string{"a"},
		ProviderOptions: map[string]any{
			"vertex": map[string]any{
				"instances":  []any{map[string]any{"content": "override"}},
				"parameters": map[string]any{"autoTruncate": false},
			},
			"other-provider": map[string]any{
				"instances": []any{map[string]any{"content": "wrong"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var instances []map[string]string
	if err := json.Unmarshal(raw["instances"], &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	if len(instances) != 1 || instances[0]["content"] != "override" {
		t.Errorf("instances = %+v, want provider option override [{content: override}]", instances)
	}

	paramsRaw, ok := raw["parameters"]
	if !ok {
		t.Fatalf("request missing novel passthrough key parameters: %s", gotBody)
	}
	var params map[string]bool
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		t.Fatalf("decode parameters: %v", err)
	}
	if params["autoTruncate"] != false {
		t.Errorf("parameters.autoTruncate = %v, want false", params["autoTruncate"])
	}
}
