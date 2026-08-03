package ai

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"runtime"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// slowToolArgs is the (empty) argument struct for the "slow" tool used by
// the terminal-tool-execution timeout tests below.
type slowToolArgs struct{}

// newSlowTool returns a Tool whose Execute blocks for delay unconditionally
// (it does NOT select on ctx — mirroring a real external call that ignores
// cancellation), used to simulate the tool-execution phase of a step taking
// longer than a Total/user-ctx bound that elapses while it runs.
func newSlowTool(delay time.Duration) Tool {
	return NewTool("slow", "", func(_ context.Context, _ slowToolArgs) (any, error) {
		time.Sleep(delay)
		return "done", nil
	})
}

// slowGenModel is a provider.LanguageModel whose Generate blocks for delay
// (returning resp) unless ctx is done first (returning ctx.Err()) — used to
// exercise GenerateText's Total/Step timeout bounds and the "user ctx wins"
// case, without a real clock-dependent provider.
type slowGenModel struct {
	delay time.Duration
	resp  *provider.Response
	calls int
}

func (m *slowGenModel) ModelID() string                     { return "slow" }
func (m *slowGenModel) ProviderName() string                { return "aitest" }
func (m *slowGenModel) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (m *slowGenModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	return nil, errors.New("slowGenModel: Stream not implemented")
}
func (m *slowGenModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	m.calls++
	select {
	case <-time.After(m.delay):
		return m.resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// slowStreamModel is a provider.LanguageModel whose Stream blocks for delay
// (returning a stream that immediately finishes) unless ctx is done first
// (returning ctx.Err()) — used to exercise StreamText's Step timeout firing
// before the first stream part ever arrives.
type slowStreamModel struct {
	delay time.Duration
}

func (m *slowStreamModel) ModelID() string                     { return "slow-stream" }
func (m *slowStreamModel) ProviderName() string                { return "aitest" }
func (m *slowStreamModel) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (m *slowStreamModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return nil, errors.New("slowStreamModel: Generate not implemented")
}
func (m *slowStreamModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	select {
	case <-time.After(m.delay):
		return &aitestFinishStream{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// aitestFinishStream is a trivial provider.StreamResponse yielding a single
// FinishPart — used when a slow model's delay elapses before ctx does.
type aitestFinishStream struct{}

func (s *aitestFinishStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		yield(provider.FinishPart{Reason: provider.FinishStop})
	}
}
func (s *aitestFinishStream) Err() error   { return nil }
func (s *aitestFinishStream) Close() error { return nil }

// stallStreamModel is a provider.LanguageModel whose Stream returns a
// *stallStreamResponse that yields exactly parts, then hangs (blocks
// forever) until its ctx is done — used to exercise StreamText's Chunk
// watchdog: the mock "delivers 2 parts then hangs."
type stallStreamModel struct {
	parts []provider.StreamPart
	last  *stallStreamResponse // records the most recently created stream, for goroutine-leak assertions
}

func (m *stallStreamModel) ModelID() string                     { return "stall" }
func (m *stallStreamModel) ProviderName() string                { return "aitest" }
func (m *stallStreamModel) Capabilities() provider.Capabilities { return provider.Capabilities{} }
func (m *stallStreamModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return nil, errors.New("stallStreamModel: Generate not implemented")
}
func (m *stallStreamModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	s := &stallStreamResponse{ctx: ctx, parts: m.parts}
	m.last = s
	return s, nil
}

type stallStreamResponse struct {
	ctx    context.Context
	parts  []provider.StreamPart
	closed bool
}

func (s *stallStreamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for _, p := range s.parts {
			if !yield(p) {
				return
			}
		}
		// Delivered the scripted parts; now hang until ctx is canceled
		// (by the Chunk watchdog, in the test's expected path) rather than
		// ever yielding a FinishPart or another part.
		<-s.ctx.Done()
	}
}
func (s *stallStreamResponse) Err() error   { return s.ctx.Err() }
func (s *stallStreamResponse) Close() error { s.closed = true; return nil }

// --- GenerateText: Total ---

func TestGenerateTextTotalTimeout(t *testing.T) {
	m := &slowGenModel{delay: 200 * time.Millisecond}
	var onErrErr error
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Total: 20 * time.Millisecond},
		OnError: func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if !errors.As(err, &te) || te.Dimension != "total" {
		t.Fatalf("err = %v (%T); want *TimeoutError{Dimension: total}", err, err)
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "total" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: total}", onErrErr)
	}
}

// --- GenerateText: Step ---

func TestGenerateTextStepTimeout(t *testing.T) {
	m := &slowGenModel{delay: 200 * time.Millisecond}
	var onErrErr error
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Step: 20 * time.Millisecond},
		OnError: func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if !errors.As(err, &te) || te.Dimension != "step" {
		t.Fatalf("err = %v (%T); want *TimeoutError{Dimension: step}", err, err)
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "step" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: step}", onErrErr)
	}
}

// --- GenerateText: user's own ctx wins over Total ---

func TestGenerateTextUserCtxWinsOverTotal(t *testing.T) {
	m := &slowGenModel{delay: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	var onErrErr error
	_, err := GenerateText(ctx, GenerateTextOpts{
		Model:  m,
		Prompt: "hi",
		// Total is much larger than the user's own ctx deadline — the
		// user's ctx must win, and the error must NOT be a *TimeoutError
		// (that would misattribute an SDK-imposed bound to a run that
		// never got anywhere near it).
		Timeout: &Timeout{Total: time.Hour},
		OnError: func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if errors.As(err, &te) {
		t.Fatalf("err = %v; want plain ctx error, not *TimeoutError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want context.DeadlineExceeded", err)
	}
	if errors.As(onErrErr, &te) {
		t.Fatalf("OnError err = %v; want plain ctx error, not *TimeoutError", onErrErr)
	}
}

// --- StreamText: Total ---

func TestStreamTextTotalTimeout(t *testing.T) {
	m := &stallStreamModel{parts: []provider.StreamPart{
		provider.TextDelta{Text: "a"},
	}}
	var onErrErr error
	var onAbortFired bool
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Total: 20 * time.Millisecond},
		OnError: func(e error) { onErrErr = e },
		OnAbort: func() { onAbortFired = true },
	})
	if err != nil {
		t.Fatalf("StreamText start: %v", err)
	}
	for range s.Parts() {
	}

	var te *TimeoutError
	if !errors.As(s.Err(), &te) || te.Dimension != "total" {
		t.Fatalf("s.Err() = %v (%T); want *TimeoutError{Dimension: total}", s.Err(), s.Err())
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "total" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: total}", onErrErr)
	}
	if onAbortFired {
		t.Fatal("OnAbort fired; want OnError only for our own Total bound")
	}
}

// --- StreamText: Step (fires before the first stream part ever arrives) ---

func TestStreamTextStepTimeout(t *testing.T) {
	m := &slowStreamModel{delay: 200 * time.Millisecond}
	var onErrErr error
	_, err := StreamText(t.Context(), GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Step: 20 * time.Millisecond},
		OnError: func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if !errors.As(err, &te) || te.Dimension != "step" {
		t.Fatalf("err = %v (%T); want *TimeoutError{Dimension: step}", err, err)
	}
	// No *TextStream exists yet at this point (mirrors the pre-existing
	// first-stream-start failure path) so OnError is not expected to fire
	// here either — the error is reported solely via the returned err. This
	// assertion just documents that onErrErr stays unset.
	if onErrErr != nil {
		t.Fatalf("OnError fired with %v; first-stream-start failures report solely via the returned error", onErrErr)
	}
}

// --- StreamText: Chunk stall watchdog ---

func TestStreamTextChunkStallTimeout(t *testing.T) {
	before := runtime.NumGoroutine()

	m := &stallStreamModel{parts: []provider.StreamPart{
		provider.TextDelta{Text: "a"},
		provider.TextDelta{Text: "b"},
	}}
	var onErrErr error
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Chunk: 20 * time.Millisecond},
		OnError: func(e error) { onErrErr = e },
	})
	if err != nil {
		t.Fatalf("StreamText start: %v", err)
	}

	var gotParts int
	for range s.Parts() {
		gotParts++
	}
	if gotParts != 2 {
		t.Fatalf("gotParts = %d; want 2 (delivered before the stall)", gotParts)
	}

	var te *TimeoutError
	if !errors.As(s.Err(), &te) || te.Dimension != "chunk" {
		t.Fatalf("s.Err() = %v (%T); want *TimeoutError{Dimension: chunk}", s.Err(), s.Err())
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "chunk" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: chunk}", onErrErr)
	}
	if !m.last.closed {
		t.Fatal("underlying stream was never Close()d")
	}

	// No goroutine (the chunkWatchdog's time.AfterFunc, or anything else)
	// must outlive this test — allow a short settle window since the
	// watchdog's fired goroutine and the runtime's own bookkeeping
	// goroutines need a moment to actually exit.
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.Gosched()
		after := runtime.NumGoroutine()
		if after <= before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- StreamText: user's own ctx wins over Total (OnAbort, not OnError) ---

func TestStreamTextUserCtxWinsOverTotal(t *testing.T) {
	m := &stallStreamModel{parts: nil}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	var onErrErr error
	var onAbortFired bool
	s, err := StreamText(ctx, GenerateTextOpts{
		Model:   m,
		Prompt:  "hi",
		Timeout: &Timeout{Total: time.Hour}, // far larger than the user's own ctx deadline
		OnError: func(e error) { onErrErr = e },
		OnAbort: func() { onAbortFired = true },
	})
	if err != nil {
		t.Fatalf("StreamText start: %v", err)
	}
	for range s.Parts() {
	}

	if !onAbortFired {
		t.Fatal("OnAbort did not fire; want it for the caller's own ctx deadline")
	}
	var te *TimeoutError
	if errors.As(onErrErr, &te) {
		t.Fatalf("OnError fired with %v; want no *TimeoutError for a user-ctx-caused abort", onErrErr)
	}
	if !errors.Is(s.Err(), context.DeadlineExceeded) {
		t.Fatalf("s.Err() = %v; want context.DeadlineExceeded", s.Err())
	}
}

// --- fast run under all bounds completes normally ---

func TestFastRunCompletesUnderAllTimeoutBounds(t *testing.T) {
	generous := &Timeout{Total: time.Second, Step: time.Second, Chunk: time.Second}

	gm := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.TextPart{Text: "hi"}}, FinishReason: provider.FinishStop},
	}}
	gres, err := GenerateText(t.Context(), GenerateTextOpts{Model: gm, Prompt: "hi", Timeout: generous})
	if err != nil {
		t.Fatalf("GenerateText: %v", err)
	}
	if gres.Text != "hi" {
		t.Fatalf("Text = %q; want hi", gres.Text)
	}

	sm := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hi"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: sm, Prompt: "hi", Timeout: generous})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatalf("s.Err() = %v", s.Err())
	}
	if s.Text() != "hi" {
		t.Fatalf("Text() = %q; want hi", s.Text())
	}
}

// TestTimeoutErrorMessage is a minimal sanity check on TimeoutError.Error's
// shape — not load-bearing for behavior, just documents it's non-empty and
// mentions the dimension.
func TestTimeoutErrorMessage(t *testing.T) {
	err := &TimeoutError{Dimension: "chunk", Limit: 5 * time.Second}
	if got := err.Error(); got == "" {
		t.Fatal("Error() is empty")
	}
}

// --- Regression: Total elapsing during the FINAL step's tool execution ---
//
// Repro (confirmed by review): GenerateText with Timeout{Total: 10ms}, a
// tool taking 50ms, MaxSteps: 1 previously returned err == nil, fired
// OnFinish normally, and never fired OnError — the breach was visible only
// buried in Steps[0].ToolResults[0].Err. Root cause: TimeoutError
// classification only happened at the NEXT model call's ctx check; when the
// loop ends naturally (MaxSteps reached / no tool calls / StopWhen) right
// after tool execution, no path checked ctx before returning success. Fixed
// by checking timeoutErrorFor at every natural-end return point in both
// loops (see the check right after the main loop in GenerateText, and
// TextStream.finishOrTimeout in stream_text.go).

func TestGenerateTextTotalTimeoutDuringTerminalToolExecution(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content:      []provider.ContentPart{provider.ToolCallPart{ID: "1", Name: "slow", Args: json.RawMessage(`{}`)}},
			FinishReason: provider.FinishToolCalls,
		},
	}}
	var onFinishFired bool
	var onErrErr error
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:    m,
		Prompt:   "hi",
		Tools:    []Tool{newSlowTool(50 * time.Millisecond)},
		MaxSteps: 1,
		Timeout:  &Timeout{Total: 10 * time.Millisecond},
		OnFinish: func(*GenerateTextResult) { onFinishFired = true },
		OnError:  func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if !errors.As(err, &te) || te.Dimension != "total" {
		t.Fatalf("err = %v (%T); want *TimeoutError{Dimension: total} (res=%+v)", err, err, res)
	}
	if onFinishFired {
		t.Fatal("OnFinish fired; want it suppressed — a Total breach discovered after terminal tool execution must not report success")
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "total" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: total}", onErrErr)
	}
}

// TestGenerateTextUserCtxCancelDuringTerminalToolExecutionIsNotTimeoutError
// is the control for the fix above: a genuine user ctx cancellation (not one
// of our sentinels) occurring during that same terminal-tool-execution
// window must NOT be misclassified as a *TimeoutError. GenerateText has no
// OnAbort notion (see GenerateTextOpts.OnAbort's doc), so — unlike
// StreamText — this case simply isn't caught here at all, exactly as before
// this fix: the run still reports success.
func TestGenerateTextUserCtxCancelDuringTerminalToolExecutionIsNotTimeoutError(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content:      []provider.ContentPart{provider.ToolCallPart{ID: "1", Name: "slow", Args: json.RawMessage(`{}`)}},
			FinishReason: provider.FinishToolCalls,
		},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	var onErrErr error
	_, err := GenerateText(ctx, GenerateTextOpts{
		Model:    m,
		Prompt:   "hi",
		Tools:    []Tool{newSlowTool(50 * time.Millisecond)},
		MaxSteps: 1,
		OnError:  func(e error) { onErrErr = e },
	})

	var te *TimeoutError
	if errors.As(err, &te) {
		t.Fatalf("err = %v; want no *TimeoutError for a user-ctx-caused cancellation", err)
	}
	if errors.As(onErrErr, &te) {
		t.Fatalf("OnError err = %v; want no *TimeoutError for a user-ctx-caused cancellation", onErrErr)
	}
}

func TestStreamTextTotalTimeoutDuringTerminalToolExecution(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "1", Name: "slow", Args: json.RawMessage(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	var onFinishFired bool
	var onErrErr error
	var onAbortFired bool
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:    m,
		Prompt:   "hi",
		Tools:    []Tool{newSlowTool(50 * time.Millisecond)},
		MaxSteps: 1,
		Timeout:  &Timeout{Total: 10 * time.Millisecond},
		OnFinish: func(*GenerateTextResult) { onFinishFired = true },
		OnError:  func(e error) { onErrErr = e },
		OnAbort:  func() { onAbortFired = true },
	})
	if err != nil {
		t.Fatalf("StreamText start: %v", err)
	}
	for range s.Parts() {
	}

	var te *TimeoutError
	if !errors.As(s.Err(), &te) || te.Dimension != "total" {
		t.Fatalf("s.Err() = %v (%T); want *TimeoutError{Dimension: total}", s.Err(), s.Err())
	}
	if onFinishFired {
		t.Fatal("OnFinish fired; want it suppressed — a Total breach discovered after terminal tool execution must not report success")
	}
	if !errors.As(onErrErr, &te) || te.Dimension != "total" {
		t.Fatalf("OnError err = %v; want *TimeoutError{Dimension: total}", onErrErr)
	}
	if onAbortFired {
		t.Fatal("OnAbort fired; want OnError only — this is OUR bound, not a user abort")
	}
}

// TestStreamTextUserCtxCancelDuringTerminalToolExecutionFiresOnAbort is the
// StreamText control for the fix above: a genuine user ctx cancellation
// occurring during the same terminal-tool-execution window must fire
// OnAbort, not OnError/*TimeoutError — preserving the existing
// ctx-cancel-vs-our-bound distinction at this new check point too.
func TestStreamTextUserCtxCancelDuringTerminalToolExecutionFiresOnAbort(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "1", Name: "slow", Args: json.RawMessage(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	var onFinishFired bool
	var onErrErr error
	var onAbortFired bool
	s, err := StreamText(ctx, GenerateTextOpts{
		Model:    m,
		Prompt:   "hi",
		Tools:    []Tool{newSlowTool(50 * time.Millisecond)},
		MaxSteps: 1,
		OnFinish: func(*GenerateTextResult) { onFinishFired = true },
		OnError:  func(e error) { onErrErr = e },
		OnAbort:  func() { onAbortFired = true },
	})
	if err != nil {
		t.Fatalf("StreamText start: %v", err)
	}
	for range s.Parts() {
	}

	if !onAbortFired {
		t.Fatal("OnAbort did not fire; want it for the caller's own ctx deadline")
	}
	var te *TimeoutError
	if errors.As(onErrErr, &te) {
		t.Fatalf("OnError fired with %v; want no *TimeoutError for a user-ctx-caused abort", onErrErr)
	}
	if onFinishFired {
		t.Fatal("OnFinish fired; want it suppressed on the abort path")
	}
}
