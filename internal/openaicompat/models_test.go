package openaicompat

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// openRouterSpeech is an abridged GET /models?output_modalities=speech
// answer from OpenRouter, plus one text model a server might still send.
const openRouterSpeech = `{"data":[
 {"id":"elevenlabs/eleven-v3","name":"ElevenLabs: Eleven v3","created":1794421740,
  "description":"TTS","context_length":0,
  "architecture":{"modality":"text->speech","input_modalities":["text"],"output_modalities":["speech"]},
  "pricing":{"prompt":"0.00003"},
  "supported_voices":["Rachel","Adam"]},
 {"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000,
  "architecture":{"input_modalities":["text","image"],"output_modalities":["text"]}}
]}`

// recorder is an httptest handler that records requests and answers with
// a fixed status and body per path.
type recorder struct {
	mu     sync.Mutex
	reqs   []*http.Request
	routes map[string]struct {
		status int
		body   string
	}
}

func (r *recorder) route(path string, status int, body string) {
	if r.routes == nil {
		r.routes = map[string]struct {
			status int
			body   string
		}{}
	}
	r.routes[path] = struct {
		status int
		body   string
	}{status, body}
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req.Clone(req.Context()))
	rt, ok := r.routes[req.URL.Path]
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rt.status)
	_, _ = w.Write([]byte(rt.body))
}

func (r *recorder) requests() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*http.Request(nil), r.reqs...)
}

func newRecorder(t *testing.T) (*recorder, string) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	return rec, srv.URL + "/api/v1"
}

func TestListModelsOpenRouterShape(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/models", 200, openRouterSpeech)
	l := NewModelLister(Config{Name: "openrouter", APIKey: "k", BaseURL: base}, "output_modalities")

	got, err := l.ListModels(t.Context(), provider.ModelListCall{
		OutputModality: "speech",
		Headers:        map[string]string{"X-Title": "test", "Authorization": "Bearer evil"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1 (the text model is filtered client-side): %+v", len(got), got)
	}
	m := got[0]
	if m.ID != "elevenlabs/eleven-v3" || m.Name != "ElevenLabs: Eleven v3" || m.Description != "TTS" {
		t.Fatalf("identity = %+v", m)
	}
	if !m.Created.Equal(time.Unix(1794421740, 0)) {
		t.Fatalf("Created = %v", m.Created)
	}
	if strings.Join(m.Voices, ",") != "Rachel,Adam" {
		t.Fatalf("Voices = %v", m.Voices)
	}
	if strings.Join(m.InputModalities, ",") != "text" || strings.Join(m.OutputModalities, ",") != "speech" {
		t.Fatalf("modalities = %v -> %v", m.InputModalities, m.OutputModalities)
	}
	if !strings.Contains(string(m.Raw), `"pricing"`) {
		t.Fatalf("Raw lacks pricing: %s", m.Raw)
	}

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.URL.Query().Get("output_modalities") != "speech" {
		t.Fatalf("request = %s %s", r.Method, r.URL)
	}
	if r.Header.Get("Authorization") != "Bearer k" || r.Header.Get("X-Title") != "test" {
		t.Fatalf("headers = %v", r.Header)
	}
}

func TestListModelsFilters(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/models", 200, openRouterSpeech)
	l := NewModelLister(Config{Name: "openrouter", BaseURL: base, OmitEmptyAuth: true}, "output_modalities")

	tests := []struct {
		name    string
		call    provider.ModelListCall
		wantIDs string
	}{
		{"none", provider.ModelListCall{}, "elevenlabs/eleven-v3,openai/gpt-4o"},
		{"all", provider.ModelListCall{OutputModality: "all"}, "elevenlabs/eleven-v3,openai/gpt-4o"},
		{"output text", provider.ModelListCall{OutputModality: "text"}, "openai/gpt-4o"},
		{"input image", provider.ModelListCall{InputModality: "image"}, "openai/gpt-4o"},
		{"no match", provider.ModelListCall{OutputModality: "video"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := l.ListModels(t.Context(), tt.call)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(got))
			for i, m := range got {
				ids[i] = m.ID
			}
			if s := strings.Join(ids, ","); s != tt.wantIDs {
				t.Fatalf("ids = %q, want %q", s, tt.wantIDs)
			}
		})
	}
	for _, r := range rec.requests() {
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("keyless lister sent Authorization %q", r.Header.Get("Authorization"))
		}
	}
}

func TestListModelsOpenAIShapeKeepsUnreportedModalities(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/v1/models", 200, `{"object":"list","data":[
		{"id":"tts-1","object":"model","created":1699046015,"owned_by":"system"},
		{"id":"Qwen/Qwen3","object":"model","created":"1700000000","owned_by":"vllm","max_model_len":32768},
		{"id":"","object":"model"}]}`)
	l := NewModelLister(Config{Name: "openai", APIKey: "k", BaseURL: strings.TrimSuffix(base, "/api/v1") + "/v1"}, "")

	got, err := l.ListModels(t.Context(), provider.ModelListCall{OutputModality: "speech"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2 (no modalities reported, id-less entry dropped)", len(got))
	}
	if got[0].OwnedBy != "system" || got[0].Created.IsZero() {
		t.Fatalf("openai model = %+v", got[0])
	}
	if got[1].ContextLength != 32768 || got[1].Created.Unix() != 1700000000 {
		t.Fatalf("vllm model = %+v", got[1])
	}
	if q := rec.requests()[0].URL.RawQuery; q != "" {
		t.Fatalf("query = %q, want none when the server has no modality param", q)
	}
}

func TestListModelsErrors(t *testing.T) {
	t.Run("no base URL", func(t *testing.T) {
		_, err := NewModelLister(Config{Name: "x"}, "").ListModels(t.Context(), provider.ModelListCall{})
		if err == nil || !strings.Contains(err.Error(), "base URL not configured") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("401", func(t *testing.T) {
		rec, base := newRecorder(t)
		rec.route("/api/v1/models", 401, `{"error":{"message":"No auth credentials found","code":401}}`)
		_, err := NewModelLister(Config{Name: "x", APIKey: "k", BaseURL: base}, "").ListModels(t.Context(), provider.ModelListCall{})
		var api *ai.APICallError
		if !errors.As(err, &api) || api.StatusCode != 401 || api.Retryable {
			t.Fatalf("err = %v, want non-retryable *ai.APICallError 401", err)
		}
		if api.Message != "No auth credentials found" {
			t.Fatalf("Message = %q", api.Message)
		}
	})
	t.Run("bad JSON", func(t *testing.T) {
		rec, base := newRecorder(t)
		rec.route("/api/v1/models", 200, `<html>`)
		_, err := NewModelLister(Config{Name: "x", BaseURL: base}, "").ListModels(t.Context(), provider.ModelListCall{})
		if err == nil || !strings.Contains(err.Error(), "decode model list") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestCheckKeyOpenRouter(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/key", 200, `{"data":{"label":"sk-or-v1-abc...xyz","limit":25,"usage":1.5,
		"limit_remaining":23.5,"is_free_tier":false,"rate_limit":{"requests":10,"interval":"10s"}}}`)
	k := NewKeyChecker(Config{Name: "openrouter", APIKey: "k", BaseURL: base}, "/key")

	info, err := k.CheckKey(t.Context(), provider.KeyCheckCall{})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detailed || info.Label != "sk-or-v1-abc...xyz" || info.FreeTier {
		t.Fatalf("info = %+v", info)
	}
	if info.Limit == nil || *info.Limit != 25 || info.Usage == nil || *info.Usage != 1.5 ||
		info.LimitRemaining == nil || *info.LimitRemaining != 23.5 {
		t.Fatalf("credit fields = %v %v %v", info.Limit, info.Usage, info.LimitRemaining)
	}
	if !strings.Contains(string(info.Raw), "rate_limit") {
		t.Fatalf("Raw = %s", info.Raw)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || reqs[0].URL.Path != "/api/v1/key" || reqs[0].Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestCheckKeyUnlimited(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/key", 200, `{"data":{"label":"l","limit":null,"usage":0,"limit_remaining":null,"is_free_tier":true}}`)
	info, err := NewKeyChecker(Config{Name: "x", APIKey: "k", BaseURL: base}, "/key").CheckKey(t.Context(), provider.KeyCheckCall{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Limit != nil || info.LimitRemaining != nil || !info.FreeTier {
		t.Fatalf("info = %+v", info)
	}
}

func TestCheckKeyRejected(t *testing.T) {
	for _, status := range []int{401, 403} {
		rec, base := newRecorder(t)
		rec.route("/api/v1/key", status, `{"error":{"message":"User not found.","code":401}}`)
		rec.route("/api/v1/models", 200, `{"data":[]}`)
		_, err := NewKeyChecker(Config{Name: "x", APIKey: "bad", BaseURL: base}, "/key").CheckKey(t.Context(), provider.KeyCheckCall{})
		var api *ai.APICallError
		if !errors.As(err, &api) || api.StatusCode != status {
			t.Fatalf("status %d: err = %v", status, err)
		}
		if n := len(rec.requests()); n != 1 {
			t.Fatalf("status %d: requests = %d, want no fallback after a rejection", status, n)
		}
	}
}

func TestCheckKeyFallsBackToModels(t *testing.T) {
	tests := []struct {
		name      string
		keyStatus int
		keyBody   string
		path      string
		wantReqs  int
	}{
		{"404", 404, "", "/key", 2},
		{"405", 405, `{}`, "/key", 2},
		{"200 other shape", 200, `{"ok":true}`, "/key", 2},
		{"no path", 0, "", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, base := newRecorder(t)
			if tt.keyStatus != 0 && tt.keyStatus != 404 {
				rec.route("/api/v1/key", tt.keyStatus, tt.keyBody)
			}
			rec.route("/api/v1/models", 200, `{"object":"list","data":[{"id":"m"}]}`)
			info, err := NewKeyChecker(Config{Name: "x", APIKey: "k", BaseURL: base}, tt.path).CheckKey(t.Context(), provider.KeyCheckCall{})
			if err != nil {
				t.Fatal(err)
			}
			if info.Detailed || info.Raw != nil {
				t.Fatalf("info = %+v, want not Detailed", info)
			}
			reqs := rec.requests()
			if len(reqs) != tt.wantReqs || reqs[len(reqs)-1].URL.Path != "/api/v1/models" {
				t.Fatalf("requests = %d, last %s", len(reqs), reqs[len(reqs)-1].URL.Path)
			}
		})
	}
}

func TestCheckKeyFallbackRejected(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/models", 401, `{"error":{"message":"Incorrect API key provided"}}`)
	_, err := NewKeyChecker(Config{Name: "openai", APIKey: "bad", BaseURL: base}, "").CheckKey(t.Context(), provider.KeyCheckCall{})
	var api *ai.APICallError
	if !errors.As(err, &api) || api.StatusCode != 401 {
		t.Fatalf("err = %v, want *ai.APICallError 401", err)
	}
}

func TestCheckKeyServerErrorNoFallback(t *testing.T) {
	rec, base := newRecorder(t)
	rec.route("/api/v1/key", 503, `{"error":{"message":"down"}}`)
	_, err := NewKeyChecker(Config{Name: "x", APIKey: "k", BaseURL: base}, "/key").CheckKey(t.Context(), provider.KeyCheckCall{})
	var api *ai.APICallError
	if !errors.As(err, &api) || api.StatusCode != 503 || !api.Retryable {
		t.Fatalf("err = %v, want retryable 503", err)
	}
	if n := len(rec.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}
