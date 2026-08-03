# Errors and retries

Every typed error the SDK returns lives in package `ai`, wraps its cause
via `Unwrap()` where it has one, and is meant to be tested for with
`errors.As`. This page is the full reference, plus the retry/backoff
mechanics every `ai.Generate*`/`ai.Stream*`/`ai.Embed*` call goes through.

## Typed error reference

### `*ai.APICallError`

```go
type APICallError struct {
	StatusCode   int
	URL          string
	ResponseBody string
	Retryable    bool
	Message      string
}
```

Returned by provider packages for a failed HTTP call. `NewAPICallError(statusCode int, url, body, message string) *APICallError`
sets `Retryable` from the status code (see
[Retryability rules](#retryability-rules) below). Implements
`IsRetryable() bool`, which is what the retry wrapper checks.

### `*ai.RetryError`

```go
type RetryError struct {
	Attempts int
	LastErr  error
}
```

Returned when every retry attempt for a call is exhausted — see
[RetryError anatomy](#retryerror-anatomy).

### `*ai.NoObjectGeneratedError`

```go
type NoObjectGeneratedError struct {
	RawText string
	Cause   error
}
```

Returned by `ai.GenerateObject`/`ai.StreamObject` when the model's output
couldn't be parsed into the target type; `RawText` holds what the model
actually returned. See [Structured output](structured-output.md).

### `*ai.NoSuchToolError`

```go
type NoSuchToolError struct {
	ToolName string
}
```

Returned (and, in the tool loop, treated as fatal — it aborts the whole
batch) when the model requests a tool name not present in `Tools`. See
[Tools](tools.md).

### `*ai.InvalidToolArgumentsError`

```go
type InvalidToolArgumentsError struct {
	ToolName string
	Args     json.RawMessage
	Cause    error
}
```

Returned when a tool call's arguments fail to unmarshal into the tool's
argument type. Unlike `NoSuchToolError`, this is recorded on the tool
result rather than aborting the batch (see
[Tools](tools.md#execution-error-taxonomy)).

### `*ai.ToolExecutionError`

```go
type ToolExecutionError struct {
	ToolName string
	Cause    error
}
```

Returned when a tool's `Execute` function itself returns an error.

### `*ai.ToolApprovalDeniedError`

```go
type ToolApprovalDeniedError struct {
	ToolName string
	Reason   string
}
```

Recorded on a `ToolResultRecord.Err` (never returned/raised directly) when a
tool call requiring approval (see `ai.RequireApproval`/`ai.ApprovalRequirer`)
was denied — via `GenerateTextOpts.Approvals` or `.ApproveToolCall`. `Error()`
omits `Reason` from the message when it's empty:
`ai: tool "delete_record" execution denied` vs.
`ai: tool "delete_record" execution denied: not allowed`. See
[Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution).

### Required-field sentinel errors

Several `ai.*Opts` structs validate required fields before making any call,
returning a plain (non-typed) sentinel error via `errors.New`:

| Error | Returned by |
|---|---|
| `ai.ErrModelRequired` | `GenerateText`, `StreamText`, `Embed`, `EmbedMany`, `GenerateImage`, `GenerateSpeech`, `Transcribe` — whenever `Model` is `nil` |
| `ai.ErrPromptRequired` | `GenerateImage`, when `Prompt` is empty |
| `ai.ErrTextRequired` | `GenerateSpeech`, when `Text` is empty |
| `ai.ErrAudioRequired` | `Transcribe`, when `Audio` is empty |

These are checked with `errors.Is`, not `errors.As` (they're sentinel
values, not types):

```go
_, err := ai.Embed(ctx, ai.EmbedOpts{Value: "hi"}) // Model left nil
if errors.Is(err, ai.ErrModelRequired) {
	// ...
}
```

### errors.As examples

```go
_, err := ai.GenerateText(ctx, opts)
if err != nil {
	var retryErr *ai.RetryError
	if errors.As(err, &retryErr) {
		fmt.Printf("gave up after %d attempts: %v\n", retryErr.Attempts, retryErr.LastErr)
	}

	var toolErr *ai.NoSuchToolError
	if errors.As(err, &toolErr) {
		fmt.Println("unknown tool:", toolErr.ToolName)
	}

	// RetryError.Unwrap() returns LastErr, so errors.As for the
	// underlying cause works directly on the *RetryError too — no need
	// to unwrap manually first.
	var apiErr *ai.APICallError
	if errors.As(err, &apiErr) {
		fmt.Println("status code:", apiErr.StatusCode)
	}
}
```

## Retryability rules

`NewAPICallError` marks a call retryable based on HTTP status code:

| Status | Retryable |
|---|---|
| `429` (rate limited) | yes |
| `408` (request timeout) | yes |
| `>= 500` (server error) | yes |
| everything else (e.g. `400`, `401`, `404`) | no |

An error is retried only if it implements `Retryable` (`IsRetryable() bool`)
**and** that method returns `true` — the retry wrapper uses `errors.As` to
find a `Retryable` implementation anywhere in the error's wrap chain, so a
wrapped `*APICallError` is still recognized. Any other error — including a
context error — is returned immediately on the first failure, with no
retries at all.

## Backoff parameters

Retries use exponential backoff with full jitter:

- **Base delay**: 500ms.
- **Growth**: doubles each attempt.
- **Cap**: 8 seconds — once the doubled delay would reach or exceed the
  cap, it's clamped to exactly 8s.
- **Jitter**: "full jitter" — the actual sleep is a uniformly random
  duration between 0 and the computed delay for that attempt, not the
  delay itself.
- **Context-aware**: a cancelled/expired `ctx` is checked before each
  attempt and during the backoff sleep; either aborts immediately and
  returns `ctx.Err()`.

## RetryError anatomy

```go
type RetryError struct {
	Attempts int   // total attempts made (initial call + retries)
	LastErr  error // the error from the final attempt
}

func (e *RetryError) Unwrap() error { return e.LastErr }
```

Every `ai.Generate*`/`ai.Stream*`/`ai.Embed*` call goes through the retry
wrapper with `MaxRetries` (default 2 — see the relevant `*Opts` struct;
`0` means "try once, no retries"). `RetryError.Attempts` counts every call
made, including the first: `MaxRetries: 2` means up to 3 total attempts,
and a `*RetryError` from exhaustion reports `Attempts: 3`. `LastErr` is
exactly the error the final attempt returned; `Unwrap()` exposes it so
`errors.Is`/`errors.As` against `LastErr`'s chain work directly on the
`*RetryError` without an extra unwrap step, as shown above.

If `MaxRetries` is `0`, a failing call's original error is returned
unchanged — it is never wrapped in a `*RetryError`, since there was nothing
to retry.

## Source of truth

- [`ai/errors.go`](../../ai/errors.go)
- [`ai/approval.go`](../../ai/approval.go) (`ApprovalRequirer`, `RequireApproval`)
- [`internal/retry/retry.go`](../../internal/retry/retry.go)
- [`ai/embed.go`](../../ai/embed.go), [`ai/generate_image.go`](../../ai/generate_image.go),
  [`ai/generate_speech.go`](../../ai/generate_speech.go),
  [`ai/transcribe.go`](../../ai/transcribe.go) (required-field sentinels)

See also: [Generating text](generating-text.md#retries-and-retryerror) for
`GenerateText`'s `MaxRetries` option; [Tools](tools.md) for the tool-call
error taxonomy in the multi-step loop.
