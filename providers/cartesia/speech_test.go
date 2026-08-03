package cartesia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateSpeech_HappyPath(t *testing.T) {
	var gotPath, gotAuth, gotVersion string
	var gotBody speechRequest
	audio := []byte("fake-mp3-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Cartesia-Version")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:     "hello world",
		Voice:    "voice-123",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/tts/bytes" {
		t.Errorf("path = %q, want /tts/bytes", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotVersion != "2024-11-13" {
		t.Errorf("Cartesia-Version = %q, want 2024-11-13", gotVersion)
	}
	if gotBody.ModelID != "sonic-2" {
		t.Errorf("model_id = %q", gotBody.ModelID)
	}
	if gotBody.Transcript != "hello world" {
		t.Errorf("transcript = %q", gotBody.Transcript)
	}
	if gotBody.Voice.Mode != "id" || gotBody.Voice.ID != "voice-123" {
		t.Errorf("voice = %+v", gotBody.Voice)
	}
	if gotBody.OutputFormat.Container != "mp3" {
		t.Errorf("output_format.container = %q, want mp3", gotBody.OutputFormat.Container)
	}
	if gotBody.OutputFormat.Encoding != "" {
		t.Errorf("output_format.encoding = %q, want empty (mp3 has no encoding field)", gotBody.OutputFormat.Encoding)
	}
	if gotBody.OutputFormat.SampleRate != 44100 {
		t.Errorf("output_format.sample_rate = %d, want 44100", gotBody.OutputFormat.SampleRate)
	}
	if gotBody.OutputFormat.BitRate != 128000 {
		t.Errorf("output_format.bit_rate = %d, want 128000", gotBody.OutputFormat.BitRate)
	}
	if gotBody.Language != "en" {
		t.Errorf("language = %q", gotBody.Language)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio = %q", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q", resp.MediaType)
	}
}

func TestGenerateSpeech_VoiceRequired(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error when Voice is empty")
	}
}

func TestGenerateSpeech_LanguageOmittedWhenEmpty(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if _, ok := raw["language"]; ok {
		t.Errorf("language should be omitted, got %v", raw["language"])
	}
}

func TestOutputFormatMapping(t *testing.T) {
	tests := []struct {
		container, wantEncoding, wantMediaType string
	}{
		{"", "mp3", "audio/mpeg"},
		{"mp3", "mp3", "audio/mpeg"},
		{"wav", "pcm_s16le", "audio/wav"},
		{"raw", "pcm_f32le", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := encodingForContainer(tt.container); got != tt.wantEncoding {
			t.Errorf("encodingForContainer(%q) = %q, want %q", tt.container, got, tt.wantEncoding)
		}
		if got := mediaTypeForContainer(tt.container); got != tt.wantMediaType {
			t.Errorf("mediaTypeForContainer(%q) = %q, want %q", tt.container, got, tt.wantMediaType)
		}
	}
}

func TestGenerateSpeech_OutputFormatWav(t *testing.T) {
	audio := []byte{0x52, 0x49, 0x46, 0x46}
	var gotBody speechRequest
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		var raw map[string]any
		json.Unmarshal(body, &raw)
		gotRaw = raw
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1", OutputFormat: "wav"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if gotBody.OutputFormat.Container != "wav" {
		t.Errorf("container = %q, want wav", gotBody.OutputFormat.Container)
	}
	if gotBody.OutputFormat.Encoding != "pcm_s16le" {
		t.Errorf("output_format.encoding = %q, want pcm_s16le", gotBody.OutputFormat.Encoding)
	}
	if resp.MediaType != "audio/wav" {
		t.Errorf("MediaType = %q, want audio/wav", resp.MediaType)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio mismatch")
	}

	of, ok := gotRaw["output_format"].(map[string]any)
	if !ok {
		t.Fatalf("output_format not an object: %v", gotRaw["output_format"])
	}
	if _, ok := of["encoding"]; !ok {
		t.Errorf("output_format missing encoding key for wav, want present: %v", of)
	}
	if _, ok := of["bit_rate"]; ok {
		t.Errorf("output_format has bit_rate key for wav, want absent: %v", of)
	}
}

// TestGenerateSpeech_OutputFormatMP3Shape pins the wire shape of the mp3
// discriminated-union variant: no "encoding" key at all (unlike wav/raw),
// but a "bit_rate" key present.
func TestGenerateSpeech_OutputFormatMP3Shape(t *testing.T) {
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		json.Unmarshal(body, &raw)
		gotRaw = raw
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	if _, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1", OutputFormat: "mp3"}); err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	of, ok := gotRaw["output_format"].(map[string]any)
	if !ok {
		t.Fatalf("output_format not an object: %v", gotRaw["output_format"])
	}
	if _, ok := of["encoding"]; ok {
		t.Errorf("output_format has encoding key for mp3, want absent: %v", of)
	}
	br, ok := of["bit_rate"].(float64)
	if !ok || br != 128000 {
		t.Errorf("output_format.bit_rate = %v, want 128000", of["bit_rate"])
	}
}

// TestGenerateSpeech_OutputFormatRawShape pins the "raw" container variant:
// encoding present (default pcm_f32le), no bit_rate key.
func TestGenerateSpeech_OutputFormatRawShape(t *testing.T) {
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		json.Unmarshal(body, &raw)
		gotRaw = raw
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	if _, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1", OutputFormat: "raw"}); err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	of, ok := gotRaw["output_format"].(map[string]any)
	if !ok {
		t.Fatalf("output_format not an object: %v", gotRaw["output_format"])
	}
	if of["encoding"] != "pcm_f32le" {
		t.Errorf("output_format.encoding = %v, want pcm_f32le", of["encoding"])
	}
	if _, ok := of["bit_rate"]; ok {
		t.Errorf("output_format has bit_rate key for raw, want absent: %v", of)
	}
}

func TestGenerateSpeech_ProviderOptionsMerge(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hi",
		Voice: "v1",
		ProviderOptions: map[string]any{
			"cartesia": map[string]any{"speed": "slow"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if raw["speed"] != "slow" {
		t.Errorf("speed = %v, want slow", raw["speed"])
	}
}

func TestGenerateSpeech_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*ai.APICallError)
	if !ok {
		t.Fatalf("expected *ai.APICallError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerateSpeech_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateSpeech(ctx, provider.SpeechCall{Text: "hi", Voice: "v1"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
