package ai_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/providers/openaicompatible"
)

// These tests pin the per-call retry switch for chat-style calls over a real
// HTTP provider: MaxRetries pointing at 0 makes exactly one request (the
// original error comes back unwrapped, so a router can fail over at once),
// while nil keeps the default of 2 retries (3 requests, *ai.RetryError).

type retryObj struct {
	A string `json:"a"`
}

func failingServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"busy"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestChatCallsMaxRetries(t *testing.T) {
	defer retry.SetBaseDelayForTest(time.Millisecond)()
	zero := 0

	calls := map[string]func(ctx context.Context, opts ai.GenerateTextOpts) error{
		"GenerateText": func(ctx context.Context, o ai.GenerateTextOpts) error {
			_, err := ai.GenerateText(ctx, o)
			return err
		},
		"StreamText": func(ctx context.Context, o ai.GenerateTextOpts) error {
			_, err := ai.StreamText(ctx, o)
			return err
		},
		"GenerateObject": func(ctx context.Context, o ai.GenerateTextOpts) error {
			_, err := ai.GenerateObject[retryObj](ctx, ai.GenerateObjectOpts{Model: o.Model, Prompt: o.Prompt, MaxRetries: o.MaxRetries})
			return err
		},
		"StreamObject": func(ctx context.Context, o ai.GenerateTextOpts) error {
			_, err := ai.StreamObject[retryObj](ctx, ai.GenerateObjectOpts{Model: o.Model, Prompt: o.Prompt, MaxRetries: o.MaxRetries})
			return err
		},
	}

	for name, call := range calls {
		for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
			t.Run(name+"/"+http.StatusText(status), func(t *testing.T) {
				srv, hits := failingServer(t, status)
				model := openaicompatible.New(srv.URL).Chat("m")

				err := call(context.Background(), ai.GenerateTextOpts{Model: model, Prompt: "hi", MaxRetries: &zero})
				var apiErr *ai.APICallError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("MaxRetries=0: want *APICallError %d, got %v", status, err)
				}
				var retryErr *ai.RetryError
				if errors.As(err, &retryErr) {
					t.Fatalf("MaxRetries=0: error must not be wrapped in *RetryError: %v", err)
				}
				if got := hits.Load(); got != 1 {
					t.Fatalf("MaxRetries=0: want exactly 1 request, got %d", got)
				}

				hits.Store(0)
				err = call(context.Background(), ai.GenerateTextOpts{Model: model, Prompt: "hi"})
				if !errors.As(err, &retryErr) || retryErr.Attempts != 3 {
					t.Fatalf("default: want *RetryError with 3 attempts, got %v", err)
				}
				if got := hits.Load(); got != 3 {
					t.Fatalf("default: want 3 requests (1 + 2 retries), got %d", got)
				}
			})
		}
	}
}
