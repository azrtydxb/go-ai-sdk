package auth

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
)

// Save writes c to path as JSON, atomically, with owner-only permissions:
// the file is mode 0600 and any parent directory it creates is 0700, so
// refresh tokens are never readable by other users.
func Save(path string, c Credentials) error {
	if path == "" {
		return errors.New("auth: credential path required")
	}
	if err := codexauth.SaveCredential(path, codexauth.Credential(c)); err != nil {
		return errors.New("auth: save credentials failed")
	}
	return nil
}

// Load reads credentials written by Save. A missing file satisfies
// errors.Is(err, fs.ErrNotExist).
func Load(path string) (Credentials, error) {
	c, err := codexauth.LoadCredential(path)
	return Credentials(c), err
}

// Source keeps one provider's credentials usable: it loads them from its
// file, refreshes them once expired, and saves rotated tokens back. It is
// safe for concurrent use. Its Token method makes it an OAuth token source
// for anthropic.WithOAuthTokenSource; for Codex use codex.WithCredentialFile,
// which wraps a Source.
type Source struct {
	provider string
	path     string
	client   *http.Client

	// gate serializes Credentials slow paths and Set (capacity 1); held
	// across file and network I/O. mu guards only the fields below and is
	// never held across I/O.
	gate    chan struct{}
	mu      sync.Mutex
	current Credentials
	unsaved bool // current holds refreshed tokens the file does not have yet
}

// NewSource returns a Source for "codex" or "anthropic" backed by the file
// at path. An empty path keeps credentials in memory only — they still
// refresh, but rotated tokens are lost on exit. A nil client uses
// http.DefaultClient.
func NewSource(provider, path string, client *http.Client) *Source {
	return &Source{provider: provider, path: path, client: client, gate: make(chan struct{}, 1)}
}

// Set replaces the current credentials — typically the result of Login or
// LoginDevice — saving them first when the Source has a path. If the save
// fails the Source is left unchanged.
func (s *Source) Set(c Credentials) error {
	// Take the refresh gate so a refresh in flight cannot overwrite c with
	// tokens derived from the credentials being replaced.
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	if s.path != "" {
		if err := Save(s.path, c); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.current, s.unsaved = c, false
	s.mu.Unlock()
	return nil
}

// snapshot returns the in-memory state under s.mu.
func (s *Source) snapshot() (c Credentials, unsaved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, s.unsaved
}

// store updates the in-memory state under s.mu.
func (s *Source) store(c Credentials, unsaved bool) {
	s.mu.Lock()
	s.current, s.unsaved = c, unsaved
	s.mu.Unlock()
}

// Credentials returns unexpired credentials, refreshing and saving them
// first when needed. It fails if there is nothing to load or refresh from.
//
// s.mu only guards the in-memory fields and is never held across file or
// network I/O, so callers with usable credentials are never blocked by a
// refresh. Callers that need a refresh queue on s.gate, which serializes
// refreshes within the process (the file lock does so across processes) and,
// unlike a mutex, lets a waiter give up when ctx ends.
func (s *Source) Credentials(ctx context.Context) (Credentials, error) {
	if cur, unsaved := s.snapshot(); !unsaved && usable(cur) {
		return cur, nil
	}
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	}
	defer func() { <-s.gate }()

	// State is re-read under the gate: whoever held it before us may have
	// refreshed already.
	cur, unsaved := s.snapshot()
	// A refresh whose save failed spent the old refresh token, so memory
	// holds the only valid copy: keep retrying the save, and keep failing
	// loudly, until the file has it.
	if unsaved {
		if err := Save(s.path, cur); err != nil {
			return Credentials{}, err
		}
		s.store(cur, false)
	}
	if usable(cur) {
		return cur, nil
	}
	// Re-read before refreshing: the file is never older than memory (Set and
	// every refresh save to it), and another process sharing it may have
	// refreshed already — refreshing again with the token it rotated away
	// would fail.
	cur, err := s.reload(cur)
	if err != nil {
		return Credentials{}, err
	}
	if usable(cur) {
		return cur, nil
	}
	if cur.Refresh == "" {
		return Credentials{}, errors.New("auth: no credentials; log in first")
	}
	if s.path != "" {
		// Refresh tokens rotate, so only one process may refresh at a time;
		// whoever waited here finds the winner's tokens on the second reload.
		unlock, err := lockFile(ctx, s.path+".lock", staleLock)
		if err != nil {
			return Credentials{}, err
		}
		defer unlock()
		if cur, err = s.reload(cur); err != nil {
			return Credentials{}, err
		}
		if usable(cur) {
			return cur, nil
		}
	}
	c, err := Refresh(ctx, s.provider, s.client, cur)
	if err != nil {
		return Credentials{}, err
	}
	if s.path != "" {
		if err := Save(s.path, c); err != nil {
			s.store(c, true)
			return Credentials{}, err
		}
	}
	s.store(c, false)
	return c, nil
}

// reload returns the file's credentials, also storing them in memory; cur is
// returned unchanged when there is no file. Only a missing file is ignored:
// an unreadable or corrupt one is reported rather than papered over by
// refreshing from a stale snapshot and overwriting it. Callers hold s.gate.
func (s *Source) reload(cur Credentials) (Credentials, error) {
	if s.path == "" {
		return cur, nil
	}
	c, err := Load(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return cur, nil
	}
	if err != nil {
		return cur, errors.New("auth: load credentials failed")
	}
	s.store(c, false)
	return c, nil
}

// staleLock is how old a lock file must be before a waiter assumes its owner
// crashed and breaks it — far longer than a token refresh takes.
const staleLock = 2 * time.Minute

// lockPoll is how often a waiter retries a held lock: short enough that a
// release is noticed promptly next to a network refresh, long enough that
// waiting costs next to no CPU.
const lockPoll = 100 * time.Millisecond

// lockFile takes a cross-process lock by exclusively creating path, waiting
// for a current holder until ctx ends. A lock file rather than flock: it is
// portable stdlib, at the price of breaking locks left by a crashed owner by
// age (stale; production callers pass staleLock). Two waiters breaking the
// same stale lock can both proceed; that needs a crash plus simultaneous
// waiters, and costs one failed refresh.
func lockFile(ctx context.Context, path string, stale time.Duration) (unlock func(), err error) {
	return lockFileWith(ctx, path, stale, lockPoll, os.Remove)
}

// lockFileWith is lockFile with the poll interval and file removal injectable.
func lockFileWith(ctx context.Context, path string, stale, poll time.Duration, remove func(string) error) (unlock func(), err error) {
	timer := time.NewTimer(poll)
	defer timer.Stop()
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("auth: lock credentials failed: %w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > stale {
			// Retry at once only if the stale lock is really gone; a failed
			// removal falls through to the sleep instead of spinning.
			if rmErr := remove(path); rmErr == nil || errors.Is(rmErr, fs.ErrNotExist) {
				continue
			}
		}
		timer.Reset(poll)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Token returns a current access token.
func (s *Source) Token(ctx context.Context) (string, error) {
	c, err := s.Credentials(ctx)
	return c.Access, err
}

func usable(c Credentials) bool {
	return c.Access != "" && time.Now().Before(c.Expires)
}

// LoginDevice completes Codex device-code login, for hosts with no browser:
// show receives the verification URL and the user code to display, then
// LoginDevice polls until the user authorizes, the code expires, or ctx ends.
// Only "codex" supports device login. A nil client uses http.DefaultClient.
func LoginDevice(ctx context.Context, provider string, client *http.Client, show func(ctx context.Context, verificationURL, userCode string) error) (Credentials, error) {
	if provider != "codex" {
		return Credentials{}, errors.New("auth: device login unsupported for provider")
	}
	if show == nil {
		return Credentials{}, errors.New("auth: show callback required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	dc, err := codexauth.StartDeviceCode(ctx, client)
	if err != nil {
		return Credentials{}, safeError(ctx, "device login", err)
	}
	if err := show(ctx, dc.VerificationURI, dc.UserCode); err != nil {
		return Credentials{}, safeError(ctx, "device login", err)
	}
	c, err := codexauth.LoginDevice(ctx, client, dc)
	if err != nil {
		return Credentials{}, safeError(ctx, "device login", err)
	}
	return Credentials(c), nil
}
