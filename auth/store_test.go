package auth_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/auth"
)

func TestSaveLoadOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "codex.json")
	want := auth.Credentials{Access: "a", Refresh: "r", Expires: time.Now().Add(time.Hour).Round(0), AccountID: "acct"}

	// A pre-existing world-readable file must be tightened, not kept.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for p, mode := range map[string]fs.FileMode{path: 0o600, dir: 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("%s mode = %o, want %o", p, got, mode)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (temp file left behind)", len(entries))
	}
	got, err := auth.Load(path)
	if err != nil || !got.Expires.Equal(want.Expires) || got.Access != want.Access || got.Refresh != want.Refresh || got.AccountID != want.AccountID {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if _, err := auth.Load(filepath.Join(dir, "missing.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestSourceRefreshesExpiredAndPersistsRotation(t *testing.T) {
	for _, provider := range []string{"codex", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cred.json")
			refreshes := 0
			client := tokenClient(t, provider, func(fields map[string]string) {
				refreshes++
				if fields["grant_type"] != "refresh_token" || fields["refresh_token"] != "old-refresh" {
					t.Errorf("refresh grant = %v", fields)
				}
			})
			source := auth.NewSource(provider, path, client)
			if _, err := source.Credentials(context.Background()); err == nil {
				t.Fatal("Credentials with nothing stored: want error")
			}
			expired := auth.Credentials{Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}
			if err := source.Set(expired); err != nil {
				t.Fatal(err)
			}

			token, err := source.Token(context.Background())
			if err != nil || token == "" || token == "old-access" {
				t.Fatalf("Token = %q, %v; want a refreshed token", token, err)
			}
			if _, err := source.Token(context.Background()); err != nil || refreshes != 1 {
				t.Fatalf("second Token: err=%v refreshes=%d, want the cached token and 1 refresh", err, refreshes)
			}
			saved, err := auth.Load(path)
			if err != nil || saved.Refresh != "rotated-refresh" || saved.Access != token {
				t.Fatalf("file after refresh = %+v, %v; want the rotated tokens", saved, err)
			}

			// A fresh Source (a new process) picks the saved credentials up
			// without logging in or refreshing again.
			again := auth.NewSource(provider, path, client)
			if got, err := again.Token(context.Background()); err != nil || got != token || refreshes != 1 {
				t.Fatalf("new Source Token = %q, %v (refreshes=%d)", got, err, refreshes)
			}
		})
	}
}

func TestSourceUsesFileRefreshedByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("refreshed although the file already held valid credentials")
		return nil, errors.New("unexpected")
	})}
	source := auth.NewSource("codex", path, client)
	if err := source.Set(auth.Credentials{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save(path, auth.Credentials{Access: "theirs", Refresh: "their-refresh", Expires: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got, err := source.Token(context.Background()); err != nil || got != "theirs" {
		t.Fatalf("Token = %q, %v; want the other process's token", got, err)
	}
}

// TestSourceRefreshIsSerializedAcrossProcesses runs several Sources on one
// file — each standing in for a separate process — against a token endpoint
// that, like the real one, rejects a refresh token it has already rotated.
func TestSourceRefreshIsSerializedAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	if err := auth.Save(path, auth.Credentials{Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	refreshes := 0
	base := tokenClient(t, "codex", func(map[string]string) {})
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		refreshes++
		first := refreshes == 1
		mu.Unlock()
		if !first {
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`))}, nil
		}
		time.Sleep(50 * time.Millisecond) // hold the refresh open so the others pile up
		return base.Transport.RoundTrip(r)
	})}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := auth.NewSource("codex", path, client).Token(context.Background()); err != nil {
				t.Errorf("Token: %v", err)
			}
		}()
	}
	wg.Wait()
	if refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestSourceLockWaitHonorsContextAndBreaksStaleLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	client := tokenClient(t, "codex", func(map[string]string) {})
	if err := auth.Save(path, auth.Credentials{Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := auth.NewSource("codex", path, client).Token(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Token behind a held lock = %v, want DeadlineExceeded", err)
	}

	old := time.Now().Add(-time.Hour) // a crashed owner's leftover
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewSource("codex", path, client).Token(context.Background()); err != nil {
		t.Fatalf("Token behind a stale lock: %v", err)
	}
}

func TestSourceReportsCorruptFileInsteadOfOverwritingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cred.json")
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("refreshed despite an unreadable credential file")
		return nil, errors.New("unexpected")
	})}
	source := auth.NewSource("codex", path, client)
	if err := source.Set(auth.Credentials{Access: "old", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Token(context.Background()); err == nil || strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("Token = %v, want a load failure", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "{corrupt" {
		t.Errorf("corrupt file was overwritten: %q", data)
	}
}

func TestSourceSaveFailures(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX directory permissions that bind the current user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cred.json")
	client := tokenClient(t, "codex", func(map[string]string) {})
	expired := auth.Credentials{Access: "old-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute)}
	readOnly := func(on bool) {
		mode := fs.FileMode(0o700)
		if on {
			mode = 0o500
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { readOnly(false) })

	// Set: a failed save leaves the Source unchanged.
	source := auth.NewSource("codex", path, client)
	readOnly(true)
	if err := source.Set(expired); err == nil {
		t.Fatal("Set into a read-only directory: want error")
	}
	if _, err := source.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("Token after failed Set = %v, want no credentials", err)
	}

	// Refresh: a failed save keeps failing until the rotated tokens reach the
	// file, then succeeds without spending the rotated-away token again. The
	// token endpoint blocks the save by turning the path into a directory.
	readOnly(false)
	refreshes := 0
	blocking := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		refreshes++
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
			t.Error(err)
		}
		return client.Transport.RoundTrip(r)
	})}
	source = auth.NewSource("codex", path, blocking)
	if err := source.Set(expired); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := source.Token(context.Background()); err == nil {
			t.Fatal("Token with an unsaved rotation: want error")
		}
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token once the path is writable: %v", err)
	}
	if refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", refreshes)
	}
	if saved, err := auth.Load(path); err != nil || saved.Access != token || saved.Refresh != "rotated-refresh" {
		t.Errorf("file = %+v, %v; want the rotated tokens", saved, err)
	}
}

func TestLoginDevice(t *testing.T) {
	var shownURL, shownCode string
	polls := 0
	inner := tokenClient(t, "codex", func(fields map[string]string) {
		if fields["grant_type"] != "authorization_code" || fields["code"] != "device-auth-code" || fields["code_verifier"] != "device-verifier" {
			t.Errorf("token exchange = %v", fields)
		}
	})
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		respond := func(status int, body string) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/usercode"):
			return respond(200, `{"device_auth_id":"dev-1","user_code":"ABCD-1234","interval":"1","expires_in":"60"}`)
		case strings.Contains(r.URL.Path, "deviceauth"):
			polls++
			if polls == 1 {
				return respond(403, `{}`) // still pending
			}
			return respond(200, `{"authorization_code":"device-auth-code","code_verifier":"device-verifier"}`)
		default:
			return inner.Transport.RoundTrip(r)
		}
	})}

	got, err := auth.LoginDevice(context.Background(), "codex", client, func(_ context.Context, verificationURL, userCode string) error {
		shownURL, shownCode = verificationURL, userCode
		return nil
	})
	if err != nil {
		t.Fatalf("LoginDevice: %v", err)
	}
	if !strings.HasPrefix(shownURL, "https://auth.openai.com/") || shownCode != "ABCD-1234" {
		t.Errorf("show got (%q, %q)", shownURL, shownCode)
	}
	if got.Refresh != "rotated-refresh" || got.AccountID != "fixture-account" || got.Expires.IsZero() || polls != 2 {
		t.Errorf("credentials = %+v after %d polls", got, polls)
	}

	if _, err := auth.LoginDevice(context.Background(), "anthropic", client, func(context.Context, string, string) error { return nil }); err == nil {
		t.Error("LoginDevice(anthropic): want unsupported error")
	}
	_, err = auth.LoginDevice(context.Background(), "codex", client, func(context.Context, string, string) error { return errors.New("RAW-SECRET-TOKEN") })
	if err == nil || strings.Contains(err.Error(), "RAW-SECRET") || errors.Unwrap(err) != nil {
		t.Errorf("unsafe show error: %v", err)
	}
}
