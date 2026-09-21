# OpenAI Codex / ChatGPT subscription auth

`providers/codex` talks directly to the ChatGPT Codex backend
(`https://chatgpt.com/backend-api/codex/responses`) with OAuth credentials from
an OpenAI/ChatGPT Plus or Pro subscription. It does **not** use an OpenAI API
key and does not shell out to the `codex` CLI.

```go
import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/auth"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

// Setup, once: log in and save the credentials (file mode 0600).
creds, err := auth.Login(ctx, "codex", nil, interaction) // or auth.LoginDevice
if err != nil {
	return err
}
if err := auth.Save(path, creds); err != nil {
	return err
}

// Every run: the provider loads the file, refreshes expired tokens
// automatically, and saves rotated tokens back.
p := codex.New(codex.WithCredentialFile(path))
model := p.Model("gpt-5.4")

result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Summarize this repository.",
})
```

## Authentication

Import `github.com/azrtydxb/go-ai-sdk/auth` for the public login API:

- `auth.Login(ctx, "codex", client, interaction)` reuses the SDK's PKCE flow
  and validates state before exchanging a code. The callback is fixed at
  `http://localhost:1455/auth/callback`, bound only to IPv4 loopback.
- `auth.Interaction` supplies `OpenURL` and optional `Prompt` callbacks.
  Manual input must include state: a full callback URL or `code#state`.
  Both callbacks must respect context cancellation. Browser completion
  cancels and joins the losing prompt. Login is bounded to five minutes or
  the caller's earlier deadline.
- If the callback port is unavailable, a supplied `Prompt` still allows
  state-validated manual login. Callback-only login fails immediately with a
  safe listener-unavailable error. State-validated provider denial callbacks
  end login promptly; invalid-state callbacks are ignored.
- `auth.LoginDevice(ctx, "codex", client, show)` is the device-code flow for
  hosts with no browser: `show` receives the verification URL and user code
  to display, and the call polls until the user authorizes, the code expires,
  or the context ends.
- `auth.Refresh(ctx, "codex", client, current)` forces a refresh and returns
  updated access and refresh tokens, expiry, and account ID.
- `auth.Save(path, creds)` and `auth.Load(path)` persist a snapshot as JSON.
  The write is atomic, the file is mode `0600` (an existing looser mode is
  tightened), and any directory it creates is `0700`.
- `auth.NewSource("codex", path, client)` combines them: it loads from the
  file, refreshes once the access token expires, saves rotated tokens back,
  and is safe for concurrent use. Before refreshing it re-reads the file, so a
  token another process already refreshed is reused; there is no cross-process
  file lock beyond that.
- A nil HTTP client uses `http.DefaultClient`. Public auth errors omit raw
  endpoint bodies and interaction errors; cancellation remains identifiable.

The public credential fields are `Access`, `Refresh`, `Expires`, and
`AccountID`. The SDK never logs them. `Login`, `LoginDevice`, and `Refresh`
write nothing to disk; only `Save`, a `Source` with a path, and
`codex.WithCredentialFile` do. The SDK never reads `~/.codex`.

Three ways to hand credentials to the provider:

- `codex.WithCredentialFile(path)` — the default choice. Loads, refreshes
  automatically, and saves rotated tokens to `path`.
- `codex.WithCredentials(codex.Credential(creds))` — an in-memory snapshot.
  With a refresh token and a non-zero `Expires` it still refreshes
  automatically once expired, but rotated tokens are lost on exit. A zero
  `Expires` is used as-is and never refreshed.
- `codex.WithCredentialSource(source)` — app-managed storage (a keyring, a
  secrets manager). The source is invoked on every Generate and Stream call,
  even on an already-created model, and takes precedence over the other two.
  The caller must make it concurrency-safe and handle refresh and persistence
  there.

## Request shape

The provider sends Responses-style requests to
`/backend-api/codex/responses` with the subscription OAuth access token:

- `Authorization: Bearer <access_token>`
- `chatgpt-account-id: <account_id>`
- `originator: go-ai-sdk`
- `OpenAI-Beta: responses=experimental`
- `User-Agent: go-ai-sdk/codex`

`Call.Headers` and provider headers may add non-auth headers; auth headers are
protected from override.

`Call.Reasoning.Effort` is forwarded as `reasoning.effort` for both Generate
and Stream. It is also available through `ai.GenerateTextOpts.Reasoning`,
including options set by `agent.Agent.PrepareOpts`. Nonempty efforts are sent
unchanged (for example `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, or
`max`); supported values depend on the selected model and are not clamped by
the SDK. An unset/empty effort keeps the existing default. Reasoning token
budgets are not mapped by this provider.

Generic sampling/output knobs such as `Temperature` and `MaxTokens` are not
forwarded to the subscription backend. `Call.ProviderOptions` is not a broad
request passthrough for Codex; do not use it to enable unsupported fields.

## Supported capabilities

- **Text generation and streaming** — `p.Model(id)` implements
  `provider.LanguageModel`.
- **Tool calling** — request conversion supports SDK tool definitions and
  streaming function-call deltas.
- **Images by URL** — user `ImagePart` values with `URL` are passed through as
  Responses API `input_image` items.
- **No API-key mode** — `codex.WithAPIKey` is a no-op compatibility shim; use
  `codex.WithCredentials` with OAuth credentials.

## Tests and live boundary

The package tests use fake HTTP/SSE servers for OAuth token exchange, refresh,
device polling, header verification, and streaming parse behavior. They do not
contact OpenAI and do not require real subscription credentials.
Behavior tests use isolated ephemeral loopback listeners while verifying that
the registered production redirect remains unchanged. Public consumer tests
use manual input and fake token transports without requiring a free fixed port.
