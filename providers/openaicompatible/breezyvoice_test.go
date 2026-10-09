package openaicompatible

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// TestSpeechBreezyVoiceContract reproduces mtkresearch/BreezyVoice api.py:
// its SpeechRequest model keeps only model/input/response_format/speed
// (voice is ignored), and it always answers a 22050 Hz WAV with
// Content-Type audio/wav, even when mp3 (our default) was requested.
// BreezyVoice needs CUDA (its Dockerfile is nvidia/cuda amd64), so this is
// verified against a faithful httptest stand-in rather than a live server.
func TestSpeechBreezyVoiceContract(t *testing.T) {
	wav := []byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x22\x56\x00\x00") // 0x5622 = 22050 Hz
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Disposition", "attachment; filename=output.wav")
		_, _ = w.Write(wav)
	}))
	defer srv.Close()

	m := New(srv.URL + "/v1").Speech("MediaTek-Research/BreezyVoice")
	for _, format := range []string{"", "mp3", "wav"} {
		res, err := ai.GenerateSpeech(t.Context(), ai.GenerateSpeechOpts{Model: m, Text: "今天天氣真好", Voice: "ignored", OutputFormat: format})
		if err != nil {
			t.Fatalf("format %q: %v", format, err)
		}
		if res.MediaType != "audio/wav" {
			t.Errorf("format %q: MediaType = %q, want audio/wav", format, res.MediaType)
		}
		if string(res.Audio) != string(wav) {
			t.Errorf("format %q: audio mismatch", format)
		}
	}
	if body["model"] != "MediaTek-Research/BreezyVoice" || body["input"] != "今天天氣真好" {
		t.Errorf("body = %v", body)
	}
}
