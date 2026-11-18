package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestModelListerAndKeyChecker(t *testing.T) {
	var gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotQuery, gotAuth = r.URL.RawQuery, r.Header.Get("Authorization")
		if gotAuth != "Bearer sk-good" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"tts-1","object":"model","created":1699046015,"owned_by":"system"}]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("sk-good"), WithBaseURL(srv.URL))
	if p.ModelLister().ProviderName() != "openai" || p.KeyChecker().ProviderName() != "openai" {
		t.Fatal("ProviderName != openai")
	}
	models, err := p.ModelLister().ListModels(t.Context(), provider.ModelListCall{OutputModality: "speech"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "tts-1" || models[0].OwnedBy != "system" {
		t.Fatalf("models = %+v", models)
	}
	if gotQuery != "" {
		t.Fatalf("query = %q, want none (OpenAI has no modality filter)", gotQuery)
	}

	info, err := p.KeyChecker().CheckKey(t.Context(), provider.KeyCheckCall{})
	if err != nil || info.Detailed {
		t.Fatalf("CheckKey = %+v, %v", info, err)
	}

	_, err = New(WithAPIKey("sk-bad"), WithBaseURL(srv.URL)).KeyChecker().CheckKey(t.Context(), provider.KeyCheckCall{})
	var api *ai.APICallError
	if !errors.As(err, &api) || api.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *ai.APICallError 401", err)
	}
}
