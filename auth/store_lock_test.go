package auth_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/auth"
)

// A caller queued behind a refresh waiting on the file lock must still honor
// its own context: nothing may block on a plain mutex while the lock is held.
func TestCredentialsWaiterHonorsContextWhileFileLockHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	expired := auth.Credentials{Access: "a", Refresh: "r", Expires: time.Now().Add(-time.Hour).Round(0)}
	if err := auth.Save(path, expired); err != nil {
		t.Fatal(err)
	}
	// A fresh lock held by "another process".
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	src := auth.NewSource("codex", path, nil)

	holderCtx, stopHolder := context.WithCancel(context.Background())
	holderDone := make(chan error, 1)
	go func() { _, err := src.Credentials(holderCtx); holderDone <- err }()
	time.Sleep(150 * time.Millisecond) // let the holder reach the lock wait

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := src.Credentials(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waiter took %v to give up", d)
	}
	stopHolder()
	if err := <-holderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("holder err = %v, want canceled", err)
	}
}

// Callers with usable credentials never wait on a refresh or its file lock.
func TestCredentialsFastPathNotBlockedByFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	src := auth.NewSource("codex", path, nil)
	good := auth.Credentials{Access: "a", Refresh: "r", Expires: time.Now().Add(time.Hour).Round(0)}
	if err := src.Set(good); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if c, err := src.Credentials(ctx); err != nil || c.Access != "a" {
				t.Errorf("Credentials = %+v, %v", c, err)
			}
		}()
	}
	wg.Wait()
}
