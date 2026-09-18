package oauthflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestWaitCallbackCancelsAndJoinsPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		callbacks := make(chan Result, 1)
		promptDone := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			result, err := Wait(context.Background(), callbacks, func(ctx context.Context, _ string) (string, error) {
				defer close(promptDone)
				<-ctx.Done()
				return "", ctx.Err()
			}, "", "expected")
			if err != nil || result.Code != "code" {
				t.Error("callback lost to blocked prompt")
			}
			select {
			case <-promptDone:
			default:
				t.Error("prompt still running after Wait")
			}
		}()
		synctest.Wait()
		callbacks <- Result{Code: "code", State: "expected"}
		<-finished
	})
}

func TestWaitDeadlineCancelsAndJoinsPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		promptDone := false
		_, err := Wait(ctx, make(chan Result), func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()
			promptDone = true
			return "", ctx.Err()
		}, "", "expected")
		if !errors.Is(err, context.DeadlineExceeded) || !promptDone {
			t.Error("deadline failed to clean up prompt")
		}
	})
}

func TestWaitSimultaneousResults(t *testing.T) {
	for range 100 {
		callbacks := make(chan Result, 1)
		callbacks <- Result{Code: "callback", State: "expected"}
		result, err := Wait(context.Background(), callbacks, func(context.Context, string) (string, error) {
			return "manual#expected", nil
		}, "", "expected")
		if err != nil || (result.Code != "callback" && result.Code != "manual") {
			t.Fatal("race lost both valid responses")
		}
	}
}
