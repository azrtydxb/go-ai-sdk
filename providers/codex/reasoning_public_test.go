package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/agent"
	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

type requestTransport func(*http.Request) (*http.Response, error)

func (f requestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAgentPrepareOptsReasoning(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "future-model-effort"} {
			name := "generate/" + effort
			if stream {
				name = "stream/" + effort
			}
			t.Run(name, func(t *testing.T) {
				var body map[string]any
				calls, credentials := 0, 0
				client := &http.Client{Transport: requestTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if r.Header.Get("Authorization") != "Bearer fake-token" {
						t.Fatal("credential source not used")
					}
					response := `{"id":"r1","output":[],"status":"completed"}`
					if stream {
						response = "data: {\"type\":\"response.completed\",\"stop_reason\":\"stop\"}\n\n"
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				model := codex.New(codex.WithHTTPClient(client), codex.WithCredentialSource(func(context.Context) (codex.Credential, error) {
					credentials++
					return codex.Credential{Access: "fake-token", AccountID: "fake-account", Expires: time.Now().Add(time.Hour)}, nil
				})).Model("arbitrary-model")
				a := agent.Agent{Model: model, MaxSteps: 1, PrepareOpts: func(opts *ai.GenerateTextOpts) {
					if effort != "" {
						opts.Reasoning = &provider.ReasoningConfig{Effort: effort}
					}
					temp, tokens, topP, topK, seed := 0.7, 123, 0.9, 5, int64(1)
					opts.Temperature, opts.MaxTokens, opts.TopP, opts.TopK, opts.Seed = &temp, &tokens, &topP, &topK, &seed
					opts.PresencePenalty, opts.FrequencyPenalty = &temp, &temp
					opts.StopSequences = []string{"stop"}
					opts.ProviderOptions = map[string]any{"openai-codex": map[string]any{"temperature": temp, "max_output_tokens": tokens, "unsafe": true}}
				}}
				if stream {
					s, err := a.Stream(t.Context(), agent.RunOpts{Prompt: "hello"})
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					for range s.Parts() {
					}
					if err := s.Err(); err != nil {
						t.Fatal(err)
					}
				} else if _, err := a.Generate(t.Context(), agent.RunOpts{Prompt: "hello"}); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || credentials != 1 {
					t.Fatalf("requests=%d credential resolutions=%d", calls, credentials)
				}
				if effort == "" {
					if _, ok := body["reasoning"]; ok {
						t.Fatalf("unexpected reasoning: %v", body["reasoning"])
					}
				} else if reasoning, ok := body["reasoning"].(map[string]any); !ok || reasoning["effort"] != effort {
					t.Fatalf("reasoning = %v, want effort %q", body["reasoning"], effort)
				}
				for _, key := range []string{"temperature", "max_output_tokens", "max_tokens", "top_p", "top_k", "seed", "presence_penalty", "frequency_penalty", "stop", "unsafe"} {
					if _, ok := body[key]; ok {
						t.Errorf("unsupported %s was forwarded", key)
					}
				}
			})
		}
	}
}
