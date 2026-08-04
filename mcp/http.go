package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/sse"
)

// defaultRetryBaseDelay is the base delay used for the transport's capped
// exponential backoff when no explicit Retry-After header is present.
const defaultRetryBaseDelay = 200 * time.Millisecond

// defaultRetryMaxDelay caps the backoff delay regardless of attempt count.
const defaultRetryMaxDelay = 10 * time.Second

// TokenProvider supplies a bearer token per request, enabling refresh and
// rotation: Token is called fresh on every Send (and on every retry
// attempt), and its result is sent as "Authorization: Bearer <token>"
// unless a custom header has been configured via WithAuthHeader, in which
// case the raw token is sent under that header instead. A TokenProvider
// overrides any static Authorization header configured via headers/
// NewStreamableHTTPTransport.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// TokenProviderFunc adapts a function to the TokenProvider interface.
type TokenProviderFunc func(ctx context.Context) (string, error)

// Token implements TokenProvider.
func (f TokenProviderFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

// HTTPOption configures the Streamable HTTP transport constructed by
// NewStreamableHTTPTransportWithOptions.
type HTTPOption func(*httpTransport)

// WithTokenProvider configures tp to supply a fresh bearer token on every
// request (see TokenProvider's doc for details). It overrides any static
// Authorization header.
func WithTokenProvider(tp TokenProvider) HTTPOption {
	return func(t *httpTransport) { t.tokenProvider = tp }
}

// WithAuthHeader sends the TokenProvider's token under the header named
// name (the raw token value, no "Bearer " prefix) instead of the default
// Authorization header. It has no effect without a TokenProvider.
func WithAuthHeader(name string) HTTPOption {
	return func(t *httpTransport) { t.authHeader = name }
}

// WithHTTPRetry enables retrying Send on transient failures: HTTP 429/503
// responses (always safe — the server either rejected or didn't process the
// request) and a conservative allowlist of pre-delivery connection errors:
// connection-refused and DNS resolution failures, plus any error surfaced
// during the dial phase (a *net.OpError with Op == "dial"). These all prove
// the request bytes never reached the server, so retrying cannot cause a
// side-effecting call (e.g. tools/call) to run twice.
//
// A generic client.Do error that isn't on that allowlist (e.g. "connection
// reset by peer" while reading the response, which can happen *after* the
// server has already processed a POST) is deliberately NOT retried: the
// transport cannot tell whether the server received and acted on the
// request, and retrying could double-execute a non-idempotent tool call.
// If your deployment needs broader retry coverage, wrap the *http.Client
// (WithHTTPClientOpt) with your own idempotency-aware retry logic instead.
//
// maxRetries is the number of retries after the initial attempt (0, the
// default, disables retrying entirely). Retries use capped exponential
// backoff, honoring a Retry-After response header when present (seconds or
// an HTTP-date), and respect ctx cancellation while backing off. 4xx
// responses other than 429, and any failure once response bytes (JSON or
// SSE) have begun being consumed, are never retried. Each retry attempt
// re-invokes the configured TokenProvider, if any, for a fresh token — the
// transport does not retry 401s itself; refreshing credentials on auth
// failure is the TokenProvider's job.
func WithHTTPRetry(maxRetries int) HTTPOption {
	return func(t *httpTransport) { t.maxRetries = maxRetries }
}

// WithHTTPClientOpt overrides the *http.Client used to send requests
// (default http.DefaultClient).
func WithHTTPClientOpt(c *http.Client) HTTPOption {
	return func(t *httpTransport) { t.client = c }
}

// withStaticHeaders configures a fixed set of headers added to every
// request, unconditionally overridden per-header by WithTokenProvider /
// WithAuthHeader when those are also set. It backs the legacy
// NewStreamableHTTPTransport constructor.
func withStaticHeaders(headers map[string]string) HTTPOption {
	return func(t *httpTransport) { t.headers = headers }
}

// recvQueueSize bounds the number of received-but-not-yet-drained messages
// an httpTransport will buffer. It's generous relative to how many messages
// a single Send's response can realistically carry (an SSE response with a
// handful of JSON-RPC messages), so Send never blocks waiting for a
// concurrent Receive under normal use.
const recvQueueSize = 64

// maxSuccessBodyBytes caps a single non-streaming (application/json) response
// body read, so a compromised or misbehaving server cannot exhaust memory by
// returning an unbounded body. 16 MiB is far above any real JSON-RPC message
// yet bounded (error-response bodies are separately capped at 64 KiB). It is a
// var (not a const) only so tests can lower it.
var maxSuccessBodyBytes int64 = 16 << 20

// httpTransport implements Transport over the MCP Streamable HTTP transport:
// each Send is a POST of one JSON-RPC message; the response is either a
// single application/json body (one received message) or a text/event-stream
// body whose SSE data events each carry one received message. Received
// messages are queued and handed out, in order, by Receive.
//
// There is no persistent connection here (unlike the stdio transport's
// framedTransport): Receive only ever yields data that a prior Send's
// response produced. This matches JSON-RPC request/response flow — a
// Client's recvLoop calls Receive to pick up the response to whatever call
// is currently in flight — but means Receive will block forever if called
// without a corresponding Send having happened (or having one in flight).
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	tokenProvider TokenProvider
	authHeader    string // header name for TokenProvider's token; "" means Authorization

	maxRetries     int           // 0 disables retrying (default)
	retryBaseDelay time.Duration // base delay for capped exponential backoff; 0 means defaultRetryBaseDelay

	// mu guards sessionID, openBodies, and closedFlag together: trackBody
	// must check closedFlag and register the body (plus drainWG.Add) as one
	// atomic step, so Close's body sweep can never miss a body whose Send
	// raced it (see trackBody's doc for why).
	mu         sync.Mutex
	sessionID  string
	openBodies map[io.Closer]struct{}
	closedFlag bool

	recvCh chan json.RawMessage

	drainWG sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// NewStreamableHTTPTransport speaks the MCP Streamable HTTP transport: each
// Send POSTs the JSON-RPC message to url (Content-Type application/json,
// Accept "application/json, text/event-stream"); responses arrive either as
// a direct application/json body or as an SSE stream (text/event-stream)
// whose events carry JSON-RPC messages — both are fed to Receive in order.
// The Mcp-Session-Id response header, when present, is captured and echoed
// on subsequent requests. headers are added to every request (e.g.
// Authorization).
//
// For an SSE response, Send hands the body off to a per-response drain
// goroutine that enqueues messages as they arrive (in arrival order) and
// returns as soon as the response headers are read — it does not block
// until the stream ends. This matters because Client serializes all Sends
// behind one mutex: draining synchronously would hold that mutex, and the
// MCP spec only says a server SHOULD close the SSE stream after sending its
// response (2025-03-26), not that it MUST, so a server that keeps the
// stream open would otherwise wedge every subsequent call. Per response,
// only one goroutine drains, so ordering within that response's messages is
// preserved; Close closes any still-open response bodies, which unblocks
// their drain goroutines' reads and lets them exit.
//
// Close marks the transport closed, closes any response bodies still being
// drained, and unblocks any blocked Receive; there is no persistent
// connection or session-termination handshake to perform beyond that.
//
// Known deviations from the full 2025-03-26 Streamable HTTP transport spec
// (v1 is scoped to tools-only MCP clients, which don't need the rest):
//   - Close does not send a DELETE to terminate the session on the server;
//     the session, if any, is simply abandoned.
//   - There is no standalone GET request opening a server-initiated SSE
//     channel, so server-initiated requests/notifications outside of a
//     POST response are not supported.
func NewStreamableHTTPTransport(url string, headers map[string]string) Transport {
	return NewStreamableHTTPTransportWithOptions(url, withStaticHeaders(headers))
}

// NewStreamableHTTPTransportWithOptions is the options-taking form of
// NewStreamableHTTPTransport: it speaks the same MCP Streamable HTTP
// transport (see NewStreamableHTTPTransport's doc for the full protocol
// description), configured by opts. Use WithTokenProvider/WithAuthHeader
// for per-request bearer auth, WithHTTPRetry to opt into retrying transient
// failures, and WithHTTPClientOpt to supply a custom *http.Client.
func NewStreamableHTTPTransportWithOptions(url string, opts ...HTTPOption) Transport {
	t := &httpTransport{
		url:        url,
		client:     http.DefaultClient,
		recvCh:     make(chan json.RawMessage, recvQueueSize),
		openBodies: make(map[io.Closer]struct{}),
		closed:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// retryableHTTPError marks a Send failure as eligible for the transport's
// opt-in retry (see WithHTTPRetry): HTTP 429/503 responses and connection
// errors. Any other error from sendOnce (including read/enqueue failures
// once a response's bytes have started being consumed) is returned as-is
// and never retried, per the at-most-once guarantee for streaming
// responses.
type retryableHTTPError struct {
	err        error
	retryAfter time.Duration // 0 means "no Retry-After hint; use backoff"
}

func (e *retryableHTTPError) Error() string { return e.err.Error() }
func (e *retryableHTTPError) Unwrap() error { return e.err }

// isNotDeliveredErr reports whether err proves the request was never
// delivered to the server, making it safe to retry without risking a
// double-execution of a non-idempotent call (see WithHTTPRetry's doc for
// the rationale). It recognizes:
//   - connection-refused (syscall.ECONNREFUSED, e.g. nothing listening on
//     the target port),
//   - DNS resolution failures (*net.DNSError, e.g. the host doesn't
//     resolve),
//   - any error surfaced during the dial phase (*net.OpError with
//     Op == "dial") — dialing happens strictly before any request bytes are
//     written, so a dial-phase failure by construction cannot have reached
//     the server.
//
// Anything else — including a connection that was established and then
// reset or timed out while the request or response was in flight — is NOT
// in this allowlist, because such an error is consistent with the server
// having already received (and, for a POST, possibly processed) the
// request before the failure occurred.
func isNotDeliveredErr(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return false
}

// Send implements Transport. When WithHTTPRetry has configured
// maxRetries > 0, Send retries a retryableHTTPError up to maxRetries times
// with capped exponential backoff (honoring a Retry-After response header
// when the failure carried one), re-invoking the TokenProvider (if any) on
// every attempt. It aborts the backoff wait, without a further attempt, if
// ctx is done or the transport is closed.
func (t *httpTransport) Send(ctx context.Context, msg json.RawMessage) error {
	select {
	case <-t.closed:
		return errors.New("mcp: transport closed")
	default:
	}

	totalAttempts := 1
	if t.maxRetries > 0 {
		totalAttempts += t.maxRetries
	}

	var lastErr error
	for attempt := 0; attempt < totalAttempts; attempt++ {
		err := t.sendOnce(ctx, msg)
		if err == nil {
			return nil
		}

		var re *retryableHTTPError
		if !errors.As(err, &re) || attempt == totalAttempts-1 {
			return err
		}
		lastErr = err

		delay := re.retryAfter
		if delay <= 0 {
			delay = t.backoffDelay(attempt)
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-t.closed:
			timer.Stop()
			return errors.New("mcp: transport closed")
		}
	}
	return lastErr
}

// sendOnce performs a single POST attempt: it builds the request fresh
// (including re-invoking the TokenProvider, if any, for a current token),
// sends it, and either enqueues the resulting message(s) or returns an
// error. HTTP 429/503 and connection errors are returned as
// *retryableHTTPError so Send can decide whether to retry; every other
// error (including any failure once response bytes have started being
// read, whether a direct JSON body or an SSE stream) is returned plainly
// and is never retried.
func (t *httpTransport) sendOnce(ctx context.Context, msg json.RawMessage) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if err := t.applyAuth(ctx, req); err != nil {
		return err
	}
	if sid := t.getSessionID(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isNotDeliveredErr(err) {
			return &retryableHTTPError{err: fmt.Errorf("mcp: http request: %w", err)}
		}
		// Not on the not-delivered allowlist: this error doesn't prove the
		// request was never seen by the server (e.g. it could have reset
		// the connection after processing a POST), so retrying here risks
		// double-executing a non-idempotent call. See WithHTTPRetry's doc.
		return fmt.Errorf("mcp: http request: %w", err)
	}

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.setSessionID(sid)
	}

	if resp.StatusCode == http.StatusAccepted {
		// Notification acknowledged; no body, no message to enqueue.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return &retryableHTTPError{
			err:        fmt.Errorf("mcp: http status %d: %s", resp.StatusCode, string(body)),
			retryAfter: retryAfter,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return fmt.Errorf("mcp: http status %d: %s", resp.StatusCode, string(body))
	}

	ctype := resp.Header.Get("Content-Type")
	mediaType := ctype
	if ctype != "" {
		if mt, _, err := mime.ParseMediaType(ctype); err == nil {
			mediaType = mt
		}
	}

	switch mediaType {
	case "text/event-stream":
		// Drain asynchronously: see the doc comment on
		// NewStreamableHTTPTransport for why Send must not block here.
		// trackBody is the authoritative closed-check (the select at the
		// top of Send is only a fast path): if Close raced this request
		// and already ran, trackBody reports that atomically and the body
		// is closed here instead of being handed to a goroutine that would
		// never see it swept.
		if !t.trackBody(resp.Body) {
			resp.Body.Close()
			return errors.New("mcp: transport closed")
		}
		go t.drainSSE(resp.Body)
		return nil
	default:
		// Track this body in openBodies too (not just the SSE case above):
		// otherwise a JSON body whose read stalls (e.g. a server that sends
		// headers and a Content-Length but then never finishes the body) is
		// not interrupted by Close, which only sweeps bodies it knows about.
		// Same atomicity rationale as trackBody's doc: if the transport
		// raced closed after the fast-path check at the top of Send, back
		// off here too.
		if !t.trackBody(resp.Body) {
			resp.Body.Close()
			return errors.New("mcp: transport closed")
		}
		defer func() {
			t.untrackBody(resp.Body)
			t.drainWG.Done()
		}()
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSuccessBodyBytes+1))
		if err != nil {
			return fmt.Errorf("mcp: read response body: %w", err)
		}
		if int64(len(body)) > maxSuccessBodyBytes {
			return fmt.Errorf("mcp: response body exceeds %d bytes", maxSuccessBodyBytes)
		}
		if len(body) == 0 {
			return nil
		}
		return t.enqueue(ctx, json.RawMessage(body))
	}
}

// applyAuth sets the request's auth header from the configured
// TokenProvider, if any, calling Token(ctx) fresh for every call (i.e.
// every Send attempt, including retries) so token refresh/rotation is
// reflected immediately. It overrides any static Authorization header
// already set from t.headers. Without a TokenProvider, it is a no-op.
func (t *httpTransport) applyAuth(ctx context.Context, req *http.Request) error {
	if t.tokenProvider == nil {
		return nil
	}
	tok, err := t.tokenProvider.Token(ctx)
	if err != nil {
		return fmt.Errorf("mcp: token provider: %w", err)
	}
	header := t.authHeader
	if header == "" {
		header = "Authorization"
		tok = "Bearer " + tok
	}
	req.Header.Set(header, tok)
	return nil
}

// backoffDelay returns the capped exponential backoff delay for the given
// zero-based retry attempt number, doubling from retryBaseDelay (or
// defaultRetryBaseDelay if unset) and capping at defaultRetryMaxDelay.
func (t *httpTransport) backoffDelay(attempt int) time.Duration {
	base := t.retryBaseDelay
	if base <= 0 {
		base = defaultRetryBaseDelay
	}
	delay := base
	for i := 0; i < attempt; i++ {
		if delay >= defaultRetryMaxDelay/2 {
			return defaultRetryMaxDelay
		}
		delay *= 2
	}
	if delay > defaultRetryMaxDelay {
		delay = defaultRetryMaxDelay
	}
	return delay
}

// parseRetryAfter parses a Retry-After header value, which per RFC 9110
// is either a non-negative integer number of seconds or an HTTP-date. It
// returns 0 (meaning "no usable hint") if v is empty or invalid, or if the
// parsed instant is not in the future.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// drainSSE reads SSE events from body, enqueuing each one's data in arrival
// order, until the stream ends, the body is closed (by Close, unblocking
// the read), or enqueue fails because the transport closed. It always
// closes body and untracks it before returning.
func (t *httpTransport) drainSSE(body io.ReadCloser) {
	defer t.drainWG.Done()
	defer t.untrackBody(body)
	defer body.Close()

	for ev, err := range sse.Scan(body) {
		if err != nil {
			return
		}
		if ev.Data == "" {
			continue
		}
		if t.enqueue(t.drainCtx(), json.RawMessage(ev.Data)) != nil {
			return
		}
	}
}

// drainCtx is the context used to enqueue messages from a background drain
// goroutine, which has no caller-supplied ctx of its own. It's cancelled
// only via t.closed (handled inside enqueue), so drains never block
// Receive/Close forever.
func (t *httpTransport) drainCtx() context.Context { return context.Background() }

// trackBody registers b as an open SSE response body and reserves a
// drainWG slot for the goroutine about to drain it, unless the transport is
// already closed, in which case it does neither and returns false.
//
// This must be one atomic step under mu: Close also takes mu to flip
// closedFlag and snapshot openBodies for its sweep, so whichever of
// trackBody/Close acquires mu first determines the outcome consistently —
// either trackBody sees closedFlag and backs off (the caller closes b
// itself), or it registers b (and calls drainWG.Add) before Close can
// possibly snapshot openBodies, so Close's sweep is guaranteed to include
// b. Without this, a body whose Send raced a concurrent Close — Send read
// closedFlag as false and proceeded, but Close's sweep already ran before
// Send got here — would be tracked by nobody: it would never be closed
// (leaking the connection) and drainWG.Wait would return before the
// eventual drain goroutine even started (since Add would race Wait too),
// so Close could return while a drain goroutine was about to start and
// block forever reading an unclosed body.
func (t *httpTransport) trackBody(b io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closedFlag {
		return false
	}
	t.openBodies[b] = struct{}{}
	t.drainWG.Add(1)
	return true
}

func (t *httpTransport) untrackBody(b io.Closer) {
	t.mu.Lock()
	delete(t.openBodies, b)
	t.mu.Unlock()
}

// enqueue hands msg to Receive, honouring ctx cancellation and Close.
func (t *httpTransport) enqueue(ctx context.Context, msg json.RawMessage) error {
	select {
	case t.recvCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.closed:
		return errors.New("mcp: transport closed")
	}
}

// Receive implements Transport.
func (t *httpTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case msg := <-t.recvCh:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, errors.New("mcp: transport closed")
	}
}

// Close implements Transport. It marks the transport closed (unblocking any
// pending Receive), closes any SSE response bodies still being drained
// (unblocking their drain goroutines' reads), and waits for those
// goroutines to finish before returning.
//
// closedFlag is flipped and openBodies snapshotted under mu, the same lock
// trackBody uses to check closedFlag and register a body — see trackBody's
// doc for why that atomicity is what makes this sweep exhaustive even
// against a Send racing this call.
func (t *httpTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closedFlag = true
		bodies := make([]io.Closer, 0, len(t.openBodies))
		for b := range t.openBodies {
			bodies = append(bodies, b)
		}
		t.mu.Unlock()
		close(t.closed)
		for _, b := range bodies {
			_ = b.Close()
		}
	})
	t.drainWG.Wait()
	return nil
}

func (t *httpTransport) getSessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

func (t *httpTransport) setSessionID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}
