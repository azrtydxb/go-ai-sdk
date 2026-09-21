package auth

import (
	"context"
	"errors"
	"net/http"
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

	mu      sync.Mutex
	current Credentials
}

// NewSource returns a Source for "codex" or "anthropic" backed by the file
// at path. An empty path keeps credentials in memory only — they still
// refresh, but rotated tokens are lost on exit. A nil client uses
// http.DefaultClient.
func NewSource(provider, path string, client *http.Client) *Source {
	return &Source{provider: provider, path: path, client: client}
}

// Set replaces the current credentials — typically the result of Login or
// LoginDevice — and saves them when the Source has a path.
func (s *Source) Set(c Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = c
	if s.path == "" {
		return nil
	}
	return Save(s.path, c)
}

// Credentials returns unexpired credentials, refreshing and saving them
// first when needed. It fails if there is nothing to load or refresh from.
func (s *Source) Credentials(ctx context.Context) (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if usable(s.current) {
		return s.current, nil
	}
	// Re-read before refreshing: the file is never older than memory (Set and
	// every refresh save to it), and another process sharing it may have
	// refreshed already — refreshing again with the token it rotated away
	// would fail.
	// debt: no cross-process file lock — two processes that both find the
	// file expired still race to refresh. Revisit if concurrent CLIs sharing
	// one credential file report spurious refresh failures.
	if s.path != "" {
		if c, err := Load(s.path); err == nil {
			s.current = c
		}
	}
	if usable(s.current) {
		return s.current, nil
	}
	if s.current.Refresh == "" {
		return Credentials{}, errors.New("auth: no credentials; log in first")
	}
	c, err := Refresh(ctx, s.provider, s.client, s.current)
	if err != nil {
		return Credentials{}, err
	}
	s.current = c
	if s.path != "" {
		if err := Save(s.path, c); err != nil {
			return Credentials{}, err
		}
	}
	return c, nil
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
