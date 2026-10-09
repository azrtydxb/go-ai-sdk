package openaicompatible

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openaicompatible")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(srv.URL).Chat("test-model"),
		ProviderName: "openaicompatible",
	})
}

func TestWithName(t *testing.T) {
	p := New("http://x/v1", WithName("vllm"))
	if got := p.Chat("m").ProviderName(); got != "vllm" {
		t.Fatalf("ProviderName = %q", got)
	}
}

func TestMissingBaseURL(t *testing.T) {
	p := New("")
	ctx := context.Background()
	if _, err := p.Chat("m").Generate(ctx, provider.Call{Messages: []provider.Message{provider.UserText("hi")}}); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Errorf("chat err = %v", err)
	}
	if _, err := p.Embedding("m").Embed(ctx, []string{"a"}); err == nil {
		t.Error("embedding: want error")
	}
	if _, err := p.Speech("m").GenerateSpeech(ctx, provider.SpeechCall{Text: "a"}); err == nil {
		t.Error("speech: want error")
	}
	if _, err := p.Transcription("m").Transcribe(ctx, provider.TranscriptionCall{Audio: []byte("a"), MediaType: "audio/wav"}); err == nil {
		t.Error("transcription: want error")
	}
	if _, err := p.Rerank("m").Rerank(ctx, provider.RerankCall{Query: "q", Documents: []string{"a"}}); err == nil {
		t.Error("rerank: want error")
	}
}

func TestChatStreamingUsageInFinalPart(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Errorf("Authorization sent without API key: %v", r.Header["Authorization"])
		}
		if r.Header.Get("X-Tenant") != "acme" {
			t.Errorf("X-Tenant = %q", r.Header.Get("X-Tenant"))
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`,
		} {
			_, _ = io.WriteString(w, "data: "+c+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	m := New(srv.URL+"/v1", WithHeader("X-Tenant", "acme")).Chat("qwen")
	sr, err := m.Stream(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sr.Close() }()
	var text string
	var fin *provider.FinishPart
	for part := range sr.Parts() {
		switch p := part.(type) {
		case provider.TextDelta:
			text += p.Text
		case provider.FinishPart:
			fin = &p
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatal(err)
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if fin == nil {
		t.Fatal("no FinishPart")
	}
	if fin.Usage.InputTokens != 7 || fin.Usage.OutputTokens != 2 || fin.Usage.TotalTokens != 9 {
		t.Errorf("usage = %+v", fin.Usage)
	}
	if gotBody["model"] != "qwen" || gotBody["stream"] != true {
		t.Errorf("body = %v", gotBody)
	}
	so, _ := gotBody["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Errorf("stream_options = %v", gotBody["stream_options"])
	}
}

func TestAPIKeyAndHeaders(t *testing.T) {
	var auth, x, call string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, x, call = r.Header.Get("Authorization"), r.Header.Get("X-Static"), r.Header.Get("X-Call")
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`)
	}))
	defer srv.Close()
	m := New(srv.URL, WithAPIKey("sk"), WithHeaders(map[string]string{"X-Static": "s", "Authorization": "evil"})).Embedding("e")
	if _, err := m.Embed(t.Context(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk" || x != "s" {
		t.Errorf("auth=%q x=%q", auth, x)
	}
	_ = call
}

func TestEmbeddingsAndUsage(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[0.1,0.2]},{"index":1,"embedding":[0.3,0.4]}],"usage":{"prompt_tokens":5,"total_tokens":5}}`)
	}))
	defer srv.Close()
	m := New(srv.URL + "/v1").Embedding("bge-m3")
	if m.MaxBatchSize() != 32 {
		t.Errorf("MaxBatchSize = %d", m.MaxBatchSize())
	}
	res, err := ai.EmbedMany(t.Context(), ai.EmbedManyOpts{Model: m, Values: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Embeddings) != 2 || res.Embeddings[1][1] != 0.4 {
		t.Errorf("embeddings = %v", res.Embeddings)
	}
	if res.Usage.InputTokens != 5 {
		t.Errorf("usage = %+v", res.Usage)
	}
	if body["model"] != "bge-m3" {
		t.Errorf("body = %v", body)
	}
}

func TestRerankOpenAIShape(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"id":"r","model":"bge-reranker","usage":{"total_tokens":42},"results":[
			{"index":0,"relevance_score":0.1},{"index":2,"relevance_score":0.9},{"index":1,"relevance_score":0.5}]}`)
	}))
	defer srv.Close()
	m := New(srv.URL+"/v1", WithAPIKey("k")).Rerank("BAAI/bge-reranker-v2-m3")
	res, err := ai.Rerank(t.Context(), ai.RerankOpts{Model: m, Query: "q", Documents: []string{"a", "b", "c"}, TopN: 2})
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "BAAI/bge-reranker-v2-m3" || body["query"] != "q" || body["top_n"] != float64(2) {
		t.Errorf("body = %v", body)
	}
	if docs, _ := body["documents"].([]any); len(docs) != 3 {
		t.Errorf("documents = %v", body["documents"])
	}
	if len(res.Results) != 2 || res.Results[0].Index != 2 || res.Results[1].Index != 1 || res.Results[0].Document != "c" {
		t.Errorf("results = %+v", res.Results)
	}
	if res.Usage.TotalTokens != 42 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestRerankTEIShape(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("Authorization sent without key")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `[{"index":1,"score":0.2},{"index":0,"score":0.8},{"index":2,"score":0.5}]`)
	}))
	defer srv.Close()
	m := New(srv.URL).Rerank("tei", WithRerankShape(RerankTEI))
	res, err := ai.Rerank(t.Context(), ai.RerankOpts{Model: m, Query: "q", Documents: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := body["documents"]; has {
		t.Errorf("TEI body must use texts: %v", body)
	}
	if texts, _ := body["texts"].([]any); len(texts) != 3 || body["query"] != "q" {
		t.Errorf("body = %v", body)
	}
	want := []int{0, 2, 1}
	for i, r := range res.Results {
		if r.Index != want[i] {
			t.Fatalf("order = %+v", res.Results)
		}
	}
}

func TestRerankErrorResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		shape RerankShape
		body  string
		want  string
	}{
		"openai": {RerankOpenAI, `{"error":{"message":"model not found"}}`, "model not found"},
		"tei":    {RerankTEI, `{"error":"Input validation error: ` + "`texts`" + ` cannot be empty","error_type":"Validation"}`, "cannot be empty"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			m := New(srv.URL).Rerank("m", WithRerankShape(tc.shape))
			_, err := m.Rerank(t.Context(), provider.RerankCall{Query: "q", Documents: []string{"a"}})
			var apiErr *ai.APICallError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 || !strings.Contains(apiErr.Message, tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSpeech(t *testing.T) {
	audio := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0xff}
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("Authorization sent without key")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(audio)
	}))
	defer srv.Close()
	m := New(srv.URL + "/v1").Speech("MediaTek-Research/BreezyVoice")
	res, err := ai.GenerateSpeech(t.Context(), ai.GenerateSpeechOpts{Model: m, Text: "你好，世界", Voice: "zh-TW-female-1", OutputFormat: "wav"})
	if err != nil {
		t.Fatal(err)
	}
	if body["model"] != "MediaTek-Research/BreezyVoice" || body["input"] != "你好，世界" || body["voice"] != "zh-TW-female-1" || body["response_format"] != "wav" {
		t.Errorf("body = %v", body)
	}
	if string(res.Audio) != string(audio) || res.MediaType != "audio/wav" {
		t.Errorf("audio=%v type=%q", res.Audio, res.MediaType)
	}
}

func TestTranscriptionBreeze(t *testing.T) {
	var fields map[string]string
	var fileLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("Authorization sent without key")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(f)
		fileLen = len(b)
		_, _ = io.WriteString(w, `{"text":"你好世界","language":"zh","duration":2.5,"segments":[{"text":"你好","start":0,"end":1.2},{"text":"世界","start":1.2,"end":2.5}]}`)
	}))
	defer srv.Close()
	m := New(srv.URL + "/v1").Transcription("MediaTek-Research/Breeze-ASR-25")
	res, err := m.Transcribe(t.Context(), provider.TranscriptionCall{Audio: []byte("RIFFdata"), MediaType: "audio/wav", Language: "zh", Prompt: "繁體中文"})
	if err != nil {
		t.Fatal(err)
	}
	if fields["model"] != "MediaTek-Research/Breeze-ASR-25" || fields["language"] != "zh" || fields["response_format"] != "verbose_json" || fields["prompt"] != "繁體中文" {
		t.Errorf("fields = %v", fields)
	}
	if fileLen != 8 {
		t.Errorf("file bytes = %d", fileLen)
	}
	if res.Text != "你好世界" || len(res.Segments) != 2 || res.Segments[1].EndSec != 2.5 || res.Language != "zh" {
		t.Errorf("res = %+v", res)
	}
}

func TestTranscriptionFormatOverride(t *testing.T) {
	var format string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		format = r.FormValue("response_format")
		_, _ = io.WriteString(w, `{"text":"hi"}`)
	}))
	defer srv.Close()
	m := New(srv.URL, WithTranscriptionFormat("json")).Transcription("whisper")
	res, err := m.Transcribe(t.Context(), provider.TranscriptionCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err != nil || res.Text != "hi" || format != "json" {
		t.Fatalf("res=%+v err=%v format=%q", res, err, format)
	}
}
