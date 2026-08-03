package fal

import (
	"net/http"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	t.Setenv("FAL_API_KEY", "")
	t.Setenv("FAL_KEY", "")
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

func TestNewFromEnvFAL_API_KEY(t *testing.T) {
	t.Setenv("FAL_API_KEY", "env-key")
	t.Setenv("FAL_KEY", "other-key")
	p := New()
	if p.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key (FAL_API_KEY takes priority)", p.apiKey)
	}
}

func TestNewFromEnvFAL_KEYFallback(t *testing.T) {
	t.Setenv("FAL_API_KEY", "")
	t.Setenv("FAL_KEY", "fallback-key")
	p := New()
	if p.apiKey != "fallback-key" {
		t.Errorf("apiKey = %q, want fallback-key", p.apiKey)
	}
}

func TestImageModel(t *testing.T) {
	p := New()
	m := p.ImageModel("fal-ai/flux/schnell")
	if m.ModelID() != "fal-ai/flux/schnell" {
		t.Errorf("ModelID() = %q", m.ModelID())
	}
	if m.ProviderName() != "fal" {
		t.Errorf("ProviderName() = %q", m.ProviderName())
	}
}

func TestErrorMessage_StringDetail(t *testing.T) {
	got := errorMessage([]byte(`{"detail":"invalid api key"}`))
	if got != "invalid api key" {
		t.Errorf("errorMessage = %q, want %q", got, "invalid api key")
	}
}

func TestErrorMessage_ValidationErrorList(t *testing.T) {
	got := errorMessage([]byte(`{"detail":[{"loc":["body","prompt"],"msg":"field required","type":"value_error.missing"}]}`))
	if got != "field required" {
		t.Errorf("errorMessage = %q, want %q", got, "field required")
	}
}

func TestErrorMessage_FallsBackToRawBody(t *testing.T) {
	body := `not json at all`
	got := errorMessage([]byte(body))
	if got != body {
		t.Errorf("errorMessage = %q, want raw body %q", got, body)
	}
}
