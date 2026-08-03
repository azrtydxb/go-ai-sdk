package assemblyai

import (
	"net/http"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "")
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
	if p.poll() != defaultPollInterval {
		t.Errorf("poll() = %v, want %v", p.poll(), defaultPollInterval)
	}
}

func TestNewWithOptions(t *testing.T) {
	custom := &http.Client{}
	p := New(
		WithAPIKey("key123"),
		WithBaseURL("https://example.test"),
		WithHTTPClient(custom),
		WithPollInterval(5*time.Millisecond),
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
	if p.poll() != 5*time.Millisecond {
		t.Errorf("poll() = %v, want 5ms", p.poll())
	}
}

func TestNewFromEnvASSEMBLYAI_API_KEY(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "env-key")
	p := New()
	if p.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key", p.apiKey)
	}
}

func TestTranscriptionModel(t *testing.T) {
	p := New()
	m := p.TranscriptionModel("universal")
	if m.ModelID() != "universal" {
		t.Errorf("ModelID() = %q", m.ModelID())
	}
	if m.ProviderName() != "assemblyai" {
		t.Errorf("ProviderName() = %q", m.ProviderName())
	}
}

func TestErrorMessage_Error(t *testing.T) {
	got := errorMessage([]byte(`{"error":"invalid api key"}`))
	if got != "invalid api key" {
		t.Errorf("errorMessage = %q, want %q", got, "invalid api key")
	}
}

func TestErrorMessage_FallsBackToRawBody(t *testing.T) {
	body := `not json at all`
	got := errorMessage([]byte(body))
	if got != body {
		t.Errorf("errorMessage = %q, want raw body %q", got, body)
	}
}
