package elevenlabs

import (
	"net/http"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "")
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
		t.Errorf("apiKey = %q, want key123", p.apiKey)
	}
	if p.baseURL != "https://example.test" {
		t.Errorf("baseURL = %q", p.baseURL)
	}
	if p.client() != custom {
		t.Error("client() should return custom client")
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "env-key")
	p := New()
	if p.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want env-key", p.apiKey)
	}
}

func TestSpeechModelAndTranscriptionModel(t *testing.T) {
	p := New()
	sm := p.SpeechModel("eleven_multilingual_v2")
	if sm.ModelID() != "eleven_multilingual_v2" {
		t.Errorf("ModelID() = %q", sm.ModelID())
	}
	if sm.ProviderName() != "elevenlabs" {
		t.Errorf("ProviderName() = %q", sm.ProviderName())
	}
	tm := p.TranscriptionModel("scribe_v1")
	if tm.ModelID() != "scribe_v1" {
		t.Errorf("ModelID() = %q", tm.ModelID())
	}
	if tm.ProviderName() != "elevenlabs" {
		t.Errorf("ProviderName() = %q", tm.ProviderName())
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"string detail", `{"detail":"bad request"}`, "bad request"},
		{"object detail", `{"detail":{"message":"invalid voice","status":"x"}}`, "invalid voice"},
		{"fallback raw", `not json`, "not json"},
		{"no detail", `{"foo":"bar"}`, `{"foo":"bar"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorMessage([]byte(tt.body))
			if got != tt.want {
				t.Errorf("errorMessage(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}
