# OpenAI Codex / ChatGPT subscription auth

`providers/codex` talks directly to the ChatGPT Codex backend
(`https://chatgpt.com/backend-api/codex/responses`) with OAuth credentials from
an OpenAI/ChatGPT Plus or Pro subscription. It does **not** use an OpenAI API
key and does not shell out to the `codex` CLI.

```go
import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

// Complete login out-of-band with codexauth.StartBrowserLogin or
// codexauth.LoginDevice, then pass the resulting credential to the provider.
cred := codexauth.Credential{
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

The auth helpers live in `internal/codexauth` because token acquisition and
refresh are SDK internals, not OpenAI API-key auth:

- `StartBrowserLogin` builds a PKCE authorization URL for
  `https://auth.openai.com/oauth/authorize` and validates the local callback.
- `ExchangeAuthorizationCode` exchanges the code at
  `https://auth.openai.com/oauth/token`.
- `StartDeviceCode` and `LoginDevice` implement the headless device-code flow
  using OpenAI's Codex device endpoints.
- `RefreshToken` refreshes access tokens and re-extracts the ChatGPT account
  ID from the access-token JWT claim
  `https://api.openai.com/auth.chatgpt_account_id`.

The credential shape is `{access, refresh, expires, accountId}` and is kept
separate from `providers/openai` API-key configuration. `SaveCredential` and
`LoadCredential` provide a minimal JSON auth-store abstraction; saved files use
0600 permissions and created parent directories use 0700 permissions.

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
