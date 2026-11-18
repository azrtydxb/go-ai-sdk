package openaicompatible

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// fakeOpenRouter answers /models and /key the way OpenRouter does, and
// rejects any key other than "good".
func fakeOpenRouter(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"elevenlabs/eleven-v3","name":"ElevenLabs: Eleven v3",
				"architecture":{"input_modalities":["text"],"output_modalities":["speech"]},
				"supported_voices":["Rachel"]}]}`))
		case "/api/v1/key":
			if r.Header.Get("Authorization") != "Bearer good" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"User not found.","code":401}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"label":"sk-or-v1-...","limit":null,"usage":0.25,"is_free_tier":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestModelListerViaAI(t *testing.T) {
	srv, paths := fakeOpenRouter(t)
	p := New(srv.URL+"/api/v1", WithAPIKey("good"), WithName("openrouter"))
	if got := p.ModelLister().ProviderName(); got != "openrouter" {
		t.Fatalf("ProviderName = %q", got)
	}
	models, err := ai.ListModels(t.Context(), ai.ListModelsOpts{Lister: p.ModelLister(), OutputModality: "speech"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "elevenlabs/eleven-v3" || len(models[0].Voices) != 1 || models[0].Voices[0] != "Rachel" {
		t.Fatalf("models = %+v", models)
	}
	if (*paths)[0] != "/api/v1/models?output_modalities=speech" {
		t.Fatalf("request = %s", (*paths)[0])
	}
}

func TestKeyCheckerViaAI(t *testing.T) {
	srv, paths := fakeOpenRouter(t)

	info, err := ai.CheckKey(t.Context(), ai.CheckKeyOpts{Checker: New(srv.URL+"/api/v1", WithAPIKey("good")).KeyChecker()})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detailed || info.Label != "sk-or-v1-..." || info.Limit != nil || info.Usage == nil || *info.Usage != 0.25 {
		t.Fatalf("info = %+v", info)
	}

	_, err = ai.CheckKey(t.Context(), ai.CheckKeyOpts{Checker: New(srv.URL+"/api/v1", WithAPIKey("bad")).KeyChecker()})
	var api *ai.APICallError
	if !errors.As(err, &api) || api.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *ai.APICallError 401", err)
	}
	if len(*paths) != 2 {
		t.Fatalf("requests = %v, want one per check (401 is not retried)", *paths)
	}
}

func TestWithKeyInfoPathEmptySkipsKeyEndpoint(t *testing.T) {
	srv, paths := fakeOpenRouter(t)
	info, err := New(srv.URL+"/api/v1", WithAPIKey("bad"), WithKeyInfoPath("")).KeyChecker().CheckKey(t.Context(), provider.KeyCheckCall{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Detailed || len(*paths) != 1 || (*paths)[0] != "/api/v1/models?" {
		t.Fatalf("info = %+v, requests = %v", info, *paths)
	}
}
