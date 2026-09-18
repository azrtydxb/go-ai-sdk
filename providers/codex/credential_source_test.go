package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

func TestCredentialSourceRotatesOnEveryRequest(t *testing.T) {
	headers := make(chan [2]string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- [2]string{r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID")}
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !body.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"fixture","output":[],"stop_reason":"stop"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"stop_reason\":\"stop\"}\n\n")
	}))
	defer server.Close()
	calls := 0
	m := codex.New(codex.WithBaseURL(server.URL), codex.WithCredentials(codex.Credential{Access: "stale"}), codex.WithCredentialSource(func(context.Context) (codex.Credential, error) {
		calls++
		return codex.Credential{Access: fmt.Sprintf("token-%d", calls), AccountID: fmt.Sprintf("account-%d", calls)}, nil
	})).Model("fixture-model")
	if calls != 0 || m.ModelID() != "fixture-model" || m.ProviderName() != "openai-codex" {
		t.Fatal("model identity changed or source resolved early")
	}
	for i := 1; i <= 4; i++ {
		call := provider.Call{Messages: []provider.Message{provider.UserText("fixture")}}
		if i%2 == 1 {
			if _, err := m.Generate(context.Background(), call); err != nil {
				t.Fatal(err)
			}
		} else {
			stream, err := m.Stream(context.Background(), call)
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Parts() {
			}
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			_ = stream.Close()
		}
		got := <-headers
		if got != [2]string{fmt.Sprintf("Bearer token-%d", i), fmt.Sprintf("account-%d", i)} {
			t.Errorf("request %d did not see rotated credentials", i)
		}
	}
	if calls != 4 {
		t.Errorf("source called %d times", calls)
	}
}

func TestCredentialSourceErrorsStopRequest(t *testing.T) {
	want := errors.New("source unavailable")
	m := codex.New(codex.WithCredentialSource(func(context.Context) (codex.Credential, error) { return codex.Credential{}, want })).Model("fixture")
	if _, err := m.Generate(context.Background(), provider.Call{}); !errors.Is(err, want) {
		t.Fatal("Generate lost source error")
	}
	if _, err := m.Stream(context.Background(), provider.Call{}); !errors.Is(err, want) {
		t.Fatal("Stream lost source error")
	}
}
