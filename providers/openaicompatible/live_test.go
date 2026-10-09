package openaicompatible

// Opt-in live tests against real self-hosted servers. Every test skips unless
// its environment variables are set, so `go test ./...` stays hermetic. See
// docs/providers/openaicompatible.md ("Live testing") for the variables and
// the docker commands that were used to bring the servers up.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func liveEnv(t *testing.T, names ...string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(names))
	for _, n := range names {
		v := os.Getenv(n)
		if v == "" {
			t.Skipf("%s not set; skipping live test", n)
		}
		out[n] = v
	}
	return out
}

func liveCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

var liveRerankDocs = []string{
	"The stock market closed higher on Friday.",
	"Paris is the capital and largest city of France.",
	"Bananas are rich in potassium.",
}

const liveRerankQuery = "What is the capital of France?"

func assertRerank(t *testing.T, res *ai.RerankResult, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(res.Results) != len(liveRerankDocs) {
		t.Fatalf("got %d results, want %d: %+v", len(res.Results), len(liveRerankDocs), res.Results)
	}
	if res.Results[0].Index != 1 {
		t.Errorf("top result index = %d, want 1 (Paris): %+v", res.Results[0].Index, res.Results)
	}
	for i := 1; i < len(res.Results); i++ {
		if res.Results[i].Score > res.Results[i-1].Score {
			t.Errorf("results not sorted by score descending: %+v", res.Results)
		}
	}
	t.Logf("results=%+v usage=%+v", res.Results, res.Usage)
}

// TestLiveRerankTEI: OPENAICOMPAT_LIVE_TEI_URL is the TEI root URL (no /v1).
func TestLiveRerankTEI(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_TEI_URL")
	m := New(env["OPENAICOMPAT_LIVE_TEI_URL"]).Rerank("tei", WithRerankShape(RerankTEI))
	res, err := ai.Rerank(liveCtx(t), ai.RerankOpts{Model: m, Query: liveRerankQuery, Documents: liveRerankDocs})
	assertRerank(t, res, err)

	top, err := ai.Rerank(liveCtx(t), ai.RerankOpts{Model: m, Query: liveRerankQuery, Documents: liveRerankDocs, TopN: 1})
	if err != nil {
		t.Fatalf("Rerank TopN: %v", err)
	}
	if len(top.Results) != 1 || top.Results[0].Index != 1 {
		t.Errorf("TopN=1 results = %+v", top.Results)
	}

	// TEI normalises scores with a sigmoid unless raw_scores is requested.
	raw, err := ai.Rerank(liveCtx(t), ai.RerankOpts{
		Model: m, Query: liveRerankQuery, Documents: liveRerankDocs,
		ProviderOptions: map[string]any{defaultName: map[string]any{"raw_scores": true}},
	})
	assertRerank(t, raw, err)
}

// TestLiveRerankOpenAI: OPENAICOMPAT_LIVE_RERANK_URL is the base URL (vLLM:
// without /v1; llama.cpp: server root), OPENAICOMPAT_LIVE_RERANK_MODEL the model.
func TestLiveRerankOpenAI(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_RERANK_URL", "OPENAICOMPAT_LIVE_RERANK_MODEL")
	m := New(env["OPENAICOMPAT_LIVE_RERANK_URL"]).Rerank(env["OPENAICOMPAT_LIVE_RERANK_MODEL"])
	res, err := ai.Rerank(liveCtx(t), ai.RerankOpts{Model: m, Query: liveRerankQuery, Documents: liveRerankDocs})
	assertRerank(t, res, err)
}

func TestLiveEmbed(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_EMBED_URL", "OPENAICOMPAT_LIVE_EMBED_MODEL")
	m := New(env["OPENAICOMPAT_LIVE_EMBED_URL"]).Embedding(env["OPENAICOMPAT_LIVE_EMBED_MODEL"])
	res, err := ai.Embed(liveCtx(t), ai.EmbedOpts{Model: m, Value: "hello world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(res.Embedding) == 0 {
		t.Fatal("empty embedding")
	}
	nonZero := false
	for _, v := range res.Embedding {
		if v != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Error("embedding is all zeros")
	}
	t.Logf("dims=%d usage=%+v", len(res.Embedding), res.Usage)

	many, err := ai.EmbedMany(liveCtx(t), ai.EmbedManyOpts{Model: m, Values: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("EmbedMany: %v", err)
	}
	if len(many.Embeddings) != 3 || len(many.Embeddings[2]) != len(res.Embedding) {
		t.Errorf("EmbedMany shape: %d embeddings", len(many.Embeddings))
	}
}

func TestLiveChatStream(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_CHAT_URL", "OPENAICOMPAT_LIVE_CHAT_MODEL")
	m := New(env["OPENAICOMPAT_LIVE_CHAT_URL"]).Chat(env["OPENAICOMPAT_LIVE_CHAT_MODEL"])
	mt := 64
	s, err := ai.StreamText(liveCtx(t), ai.GenerateTextOpts{
		Model:     m,
		Prompt:    "Say hello in one short sentence.",
		MaxTokens: &mt,
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	defer func() { _ = s.Close() }()
	deltas := 0
	for p := range s.Parts() {
		if _, ok := p.(provider.TextDelta); ok {
			deltas++
		}
	}
	if err := s.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if strings.TrimSpace(s.Text()) == "" || deltas == 0 {
		t.Errorf("empty stream: text=%q deltas=%d", s.Text(), deltas)
	}
	if u := s.Usage(); u.OutputTokens <= 0 {
		t.Errorf("no usage reported: %+v", u)
	}
	t.Logf("text=%q usage=%+v finish=%v", s.Text(), s.Usage(), s.FinishReason())
}

// TestLiveTranscribe: OPENAICOMPAT_LIVE_STT_AUDIO is a path to an audio file
// (WAV unless OPENAICOMPAT_LIVE_STT_MEDIATYPE says otherwise);
// OPENAICOMPAT_LIVE_STT_LANG optionally sets the language (for example zh).
func TestLiveTranscribe(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_STT_URL", "OPENAICOMPAT_LIVE_STT_MODEL", "OPENAICOMPAT_LIVE_STT_AUDIO")
	audio, err := os.ReadFile(env["OPENAICOMPAT_LIVE_STT_AUDIO"])
	if err != nil {
		t.Fatalf("read audio: %v", err)
	}
	mediaType := os.Getenv("OPENAICOMPAT_LIVE_STT_MEDIATYPE")
	if mediaType == "" {
		mediaType = "audio/wav"
	}
	lang := os.Getenv("OPENAICOMPAT_LIVE_STT_LANG")
	for _, format := range []string{"", "text"} {
		t.Run("format="+format, func(t *testing.T) {
			var opts []Option
			if format != "" {
				opts = append(opts, WithTranscriptionFormat(format))
			}
			m := New(env["OPENAICOMPAT_LIVE_STT_URL"], opts...).Transcription(env["OPENAICOMPAT_LIVE_STT_MODEL"])
			res, err := ai.Transcribe(liveCtx(t), ai.TranscribeOpts{Model: m, Audio: audio, MediaType: mediaType, Language: lang})
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			if strings.TrimSpace(res.Text) == "" {
				t.Fatal("empty transcript")
			}
			if format == "" && len(res.Segments) == 0 {
				t.Error("verbose_json returned no segments")
			}
			t.Logf("text=%q lang=%q dur=%v segments=%d", res.Text, res.Language, res.DurationSec, len(res.Segments))
		})
	}
}

// TestLiveSpeech: OPENAICOMPAT_LIVE_TTS_OPTS may hold a "k=v,k=v" list of
// extra string fields (for example lang_code=cmn) sent verbatim.
func TestLiveSpeech(t *testing.T) {
	env := liveEnv(t, "OPENAICOMPAT_LIVE_TTS_URL", "OPENAICOMPAT_LIVE_TTS_MODEL", "OPENAICOMPAT_LIVE_TTS_VOICE")
	extra := map[string]any{}
	for _, kv := range strings.Split(os.Getenv("OPENAICOMPAT_LIVE_TTS_OPTS"), ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			extra[k] = v
		}
	}
	m := New(env["OPENAICOMPAT_LIVE_TTS_URL"]).Speech(env["OPENAICOMPAT_LIVE_TTS_MODEL"])
	res, err := ai.GenerateSpeech(liveCtx(t), ai.GenerateSpeechOpts{
		Model:           m,
		Text:            "Hello, this is a live test of speech synthesis.",
		Voice:           env["OPENAICOMPAT_LIVE_TTS_VOICE"],
		OutputFormat:    "wav",
		ProviderOptions: map[string]any{defaultName: extra},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if len(res.Audio) < 1000 {
		t.Errorf("audio too small: %d bytes", len(res.Audio))
	}
	if !strings.HasPrefix(res.MediaType, "audio/") {
		t.Errorf("media type = %q, want audio/*", res.MediaType)
	}
	t.Logf("bytes=%d type=%s", len(res.Audio), res.MediaType)
}
