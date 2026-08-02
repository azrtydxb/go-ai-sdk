package elevenlabs

// Request-shape tests for ProviderOptions wiring: (a) an option key
// overriding an SDK-built field, (b) a novel passthrough key not otherwise
// exposed by this SDK. Speech is a JSON body (merge); transcription is
// multipart (extra form fields).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestSpeechProviderOptionsOverridesAndPassthrough(t *testing.T) {
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fake-audio"))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	speed := 1.0
	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hello",
		Voice: "myvoice",
		Speed: &speed,
		ProviderOptions: map[string]any{
			"elevenlabs": map[string]any{
				"voice_settings": map[string]any{"speed": 2.0, "stability": 0.3},
				"model_id":       "eleven_turbo_v2_5",
				"seed":           42,
			},
			"other-provider": map[string]any{
				"model_id": "other-model",
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var vs map[string]float64
	if err := json.Unmarshal(raw["voice_settings"], &vs); err != nil {
		t.Fatalf("decode voice_settings: %v", err)
	}
	if vs["speed"] != 2.0 {
		t.Errorf("voice_settings.speed = %v, want provider option override 2.0", vs["speed"])
	}

	var modelID string
	if err := json.Unmarshal(raw["model_id"], &modelID); err != nil {
		t.Fatalf("decode model_id: %v", err)
	}
	if modelID != "eleven_turbo_v2_5" {
		t.Errorf("model_id = %q, want provider option override eleven_turbo_v2_5", modelID)
	}

	seedRaw, ok := raw["seed"]
	if !ok {
		t.Fatalf("request missing novel passthrough key seed: %s", gotBody)
	}
	var seed float64
	if err := json.Unmarshal(seedRaw, &seed); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	if seed != 42 {
		t.Errorf("seed = %v, want 42", seed)
	}
}

func TestTranscriptionProviderOptionsExtraFormField(t *testing.T) {
	var gotFields map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		gotFields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				gotFields[k] = v[0]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"hi","language_code":"en","words":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio"),
		MediaType: "audio/mpeg",
		ProviderOptions: map[string]any{
			"elevenlabs": map[string]any{
				"tag_audio_events": false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if got := gotFields["tag_audio_events"]; got != "false" {
		t.Errorf("tag_audio_events form field = %q, want %q", got, "false")
	}
}
