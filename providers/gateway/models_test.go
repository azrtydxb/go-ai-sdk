package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestModelLister(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"anthropic/claude-sonnet-4","object":"model",
			"created":1755815280,"owned_by":"anthropic","name":"Claude Sonnet 4","description":"d",
			"context_window":200000,"type":"language","pricing":{"input":"0.000003"}}]}`))
	}))
	defer srv.Close()

	l := New(WithAPIKey("gw"), WithBaseURL(srv.URL)).ModelLister()
	if l.ProviderName() != "gateway" {
		t.Fatalf("ProviderName = %q", l.ProviderName())
	}
	models, err := l.ListModels(t.Context(), provider.ModelListCall{})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v", models)
	}
	m := models[0]
	if m.ID != "anthropic/claude-sonnet-4" || m.Name != "Claude Sonnet 4" || m.ContextLength != 200000 || m.OwnedBy != "anthropic" {
		t.Fatalf("model = %+v", m)
	}
	if !strings.Contains(string(m.Raw), `"type":"language"`) {
		t.Fatalf("Raw = %s", m.Raw)
	}
	if gotAuth != "Bearer gw" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}
