# OpenAI Codex / ChatGPT subscription auth

`providers/codex` talks directly to the ChatGPT Codex backend
(`https://chatgpt.com/backend-api/codex/responses`) with OAuth credentials from
an OpenAI/ChatGPT Plus or Pro subscription. It does **not** use an OpenAI API
key and does not shell out to the `codex` CLI.

```go
import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

// Complete auth.Login(ctx, "codex", client, interaction) during setup,
// securely persist the result, then load the credential for requests.
cred := codex.Credential{
	Access:    "eyJ...",
	Refresh:   "...",
	AccountID: "acct_...",
}

p := codex.New(codex.WithCredentials(cred))
model := p.Model("gpt-5.4")

result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Summarize this repository.",
})
```

## Authentication

Import `github.com/azrtydxb/go-ai-sdk/auth` for the public browser-login API:

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
- `auth.Refresh(ctx, "codex", client, current)` returns updated access and
  refresh tokens, expiry, and account ID for caller persistence.
- A nil HTTP client uses `http.DefaultClient`. Public auth errors omit raw
  endpoint bodies and interaction errors; cancellation remains identifiable.

The public credential fields are `Access`, `Refresh`, `Expires`, and
`AccountID`. The SDK does not log or persist them through this API. The caller
owns secure storage and must persist rotated refresh tokens. Device login is
not exposed by this facade.

`codex.WithCredentials(codex.Credential(creds))` configures a fixed snapshot.
For app-managed refresh, use `codex.WithCredentialSource(source)`: the source
is invoked on every Generate and Stream call, even on an already-created
model, and takes precedence over the fixed snapshot. The caller must make
the source concurrency-safe and handle refresh and persistence there.

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
