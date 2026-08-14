package replicate

import (
	"net/http"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	t.Setenv("REPLICATE_API_TOKEN", "")
	p := New()
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.apiKey != "" {
		t.Errorf("apiKey = %q, want empty", p.apiKey)
	}
	if p.client() != http.DefaultClient {
		t.Error("client() should default to http.DefaultClient")
	}
}

func TestNewWithOptions(t *testing.T) {
	custom := &http.Client{}
	p := New(
		WithAPIKey("key123"),
		WithBaseURL("https://example.test"),
		WithHTTPClient(custom),
	)
	if p.apiKey != "key123" {
		t.Errorf("apiKey = %q", p.apiKey)
	}
	if p.baseURL != "https://example.test" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.client() != custom {
		t.Error("client() should return custom client")
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("REPLICATE_API_TOKEN", "env-token")
	p := New()
	if p.apiKey != "env-token" {
		t.Errorf("apiKey = %q, want env-token", p.apiKey)
	}
}

func TestImageModel(t *testing.T) {
	p := New()
	m := p.ImageModel("black-forest-labs/flux-schnell")
	if m.ModelID() != "black-forest-labs/flux-schnell" {
		t.Errorf("ModelID() = %q", m.ModelID())
	}
	if m.ProviderName() != "replicate" {
		t.Errorf("ProviderName() = %q", m.ProviderName())
	}
}
