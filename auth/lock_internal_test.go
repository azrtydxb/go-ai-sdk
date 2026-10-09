package auth

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func staleLockFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.lock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLockFileStaleRemoveFailureDoesNotSpin(t *testing.T) {
	path := staleLockFile(t)
	var calls atomic.Int64
	remove := func(string) error { calls.Add(1); return errors.New("read-only") }

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := lockFileWith(ctx, path, time.Minute, 50*time.Millisecond, remove)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	// About 6 polls fit in 300ms; a spin would make thousands of attempts.
	if n := calls.Load(); n > 20 {
		t.Fatalf("remove attempted %d times in 300ms: lockFile is spinning", n)
	}
}

func TestLockFileStaleRemoveSucceedsRetriesAtOnce(t *testing.T) {
	path := staleLockFile(t)
	start := time.Now()
	unlock, err := lockFileWith(context.Background(), path, time.Minute, 5*time.Second, os.Remove)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if time.Since(start) > time.Second {
		t.Fatal("breaking a stale lock waited for the poll interval")
	}
}

func TestLockFileOpenErrorIsWrapped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "x.lock")
	_, err := lockFile(context.Background(), path, staleLock)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist", err)
	}
}
