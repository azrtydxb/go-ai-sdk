# Anthropic

`providers/anthropic` talks to Anthropic's Messages API (`/v1/messages`)
directly. Anthropic has no embeddings API, so this package intentionally
does not implement `provider.EmbeddingModel`.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/anthropic"

p := anthropic.New(
	anthropic.WithAPIKey("sk-ant-..."),              // defaults to os.Getenv("ANTHROPIC_API_KEY")
	anthropic.WithBaseURL("https://api.anthropic.com"), // the default
	anthropic.WithHTTPClient(http.DefaultClient),
)

model := p.Model("claude-sonnet-5")
```

By default, every request is sent with two headers, not an
`Authorization: Bearer` header: `x-api-key: <key>` and
`anthropic-version: 2023-06-01` (a constant baked into the package, not
configurable via an `Option`).

## Authentication

`providers/anthropic` supports two distinct authentication modes:

- **API key mode** — `anthropic.WithAPIKey`, defaulting to
  `ANTHROPIC_API_KEY`, sends `x-api-key: <key>`.
- **Claude Pro/Max OAuth mode** — `anthropic.WithOAuthTokenSource` sends
  `Authorization: Bearer <access-token>` from a Claude subscription OAuth
  token source and does not send `x-api-key`. OAuth requests also include the
  Claude Code identity headers/betas (`x-app: cli`, `claude-code-20250219`,
  `oauth-2025-04-20`) and prepend the Claude Code system identity, matching
  Pi's direct Claude Pro/Max transport.

`p.AuthMode()` returns `"api-key"` or `"oauth"` so diagnostics can clearly
separate Anthropic API-key usage from Claude Pro/Max subscription OAuth usage.
OAuth authentication does not guarantee included plan usage. Current
[Pi documentation](https://github.com/earendil-works/pi/blob/46c9de402bddf46b03c3b9f46487b777aaa41861/packages/coding-agent/docs/providers.md)
states that third-party harness usage draws from paid extra usage, not included
Claude Pro/Max limits. Verify the provider's terms and account usage settings.
The public `github.com/azrtydxb/go-ai-sdk/auth` package reuses the SDK's PKCE
browser flow against `https://claude.ai/oauth/authorize` and exchange/refresh
at `https://platform.claude.com/v1/oauth/token`. Call
`auth.Login(ctx, "anthropic", client, interaction)` during setup, and
`auth.Refresh(ctx, "anthropic", client, current)` to force a refresh. Both
return credential snapshots and write nothing to disk. `auth.Save(path, creds)`
persists a snapshot (atomic write, file mode `0600`, created directories
`0700`), and `auth.NewSource("anthropic", path, client)` is a ready-made token
source: it loads from the file, refreshes once the access token expires, saves
rotated tokens back, and is safe for concurrent use, including by several
processes sharing the file (refreshes are serialized through a `<path>.lock`
file). Never log credentials;
the SDK never reads `~/.claude`.

The callback is fixed at `http://localhost:53692/callback`, bound to IPv4
loopback. Browser and manual input race; manual input must include the
independent OAuth state as a full callback URL or `code#state`. Interaction
callbacks must honor cancellation, and a successful browser callback cancels
and joins the losing prompt. Login is bounded to five minutes or the caller's
shorter deadline. Public errors omit raw endpoint bodies and interaction
errors; cancellation remains identifiable. If the callback port is unavailable,
a supplied `Prompt` still permits state-validated manual login; callback-only
login fails immediately with a safe listener-unavailable error. Valid-state
provider denial callbacks end login promptly, while invalid-state callbacks
cannot terminate a legitimate login. Tests use fake transports and isolated
ephemeral loopback listeners, not real credentials or fixed-port fixtures.

```go
import (
	"github.com/azrtydxb/go-ai-sdk/auth"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

// Setup, once: creds, err := auth.Login(ctx, "anthropic", nil, interaction),
// then auth.Save(path, creds). Every run:
p := anthropic.New(anthropic.WithOAuthTokenSource(auth.NewSource("anthropic", path, nil)))
```

For app-managed storage (a keyring, a secrets manager), any type with a
`Token` method works instead:

```go
import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

// WithOAuthTokenSource accepts this shape without internal imports.
type tokenSource func(context.Context) (string, error)

func (f tokenSource) Token(ctx context.Context) (string, error) { return f(ctx) }

func oauthProvider(loadToken tokenSource) *anthropic.Provider {
	return anthropic.New(anthropic.WithOAuthTokenSource(loadToken))
}
```

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)` → `provider.LanguageModel`.
- **Tool calling** — same `Model(id)`; forced single-tool-call is also how
  structured output is implemented (see below).
- **Structured output** — no native JSON mode:
  `languageModel.Capabilities().NativeJSON` is `false`, so `ai.GenerateObject`
  falls back to its tool-mode path — it injects a single `ToolDef` built from
  the target schema and sets `ToolChoice{Mode: provider.ToolChoiceTool}` to
  force that call, then decodes the tool-call arguments as the object.
- **Extended thinking** — see below.
- **No embeddings, no images, no speech, no transcription** — not exposed by
  this package.
- **Files and skills** — `p.Files()` → `provider.FileStore`, plus a
  provider-specific `p.UploadSkill`/`p.DeleteSkill` (no generic interface).
  See [Files and skills](#files-and-skills) below.

## Quirks and notes

- **`max_tokens` defaults to 4096.** `Call.MaxTokens` is a `*int`; when the
  caller leaves it `nil`, `buildMessagesRequest` substitutes `defaultMaxTokens
= 4096` rather than omitting the field — the Messages API requires
  `max_tokens` on every request, so there's no "let the API default it"
  option the way there is for temperature or top_p.
  (`providers/anthropic/anthropic.go:34`, `providers/anthropic/wire.go:210-213`.)
- **Structured output is tool-mode, not native JSON.** `Capabilities()`
  returns `provider.Capabilities{NativeJSON: false}`
  (`providers/anthropic/language_model.go:26-28`); `ai.GenerateObject`'s
  `buildObjectCall` checks that flag and, when false, builds a forced
  tool-choice call instead of setting `ResponseFormat` (`ai/generate_object.go`,
  `buildObjectCall`).
- **Thinking blocks must lead the assistant message on replay.** When an
  assistant turn containing `provider.ReasoningPart`s is sent back on a later
  turn, `assistantBlocks` partitions reasoning parts out and prepends them
  ahead of every other block type — text, tool calls — because the Messages
  API requires `thinking`/`redacted_thinking` blocks to come first in an
  assistant turn. A non-redacted reasoning part with no `Signature` can't
  form a valid replayable block and is silently skipped.
  (`providers/anthropic/wire.go:344-385`, doc comment on `assistantBlocks`.)
- **PDF is the only supported `FilePart` media type.** A `FilePart` with any
  `MediaType` other than `application/pdf` (matched case-insensitively, MIME
  parameters ignored) returns an error rather than being dropped; `Filename`,
  if set, becomes the `document` block's `title`.
  (`providers/anthropic/wire.go:289-342`, `isPDFMediaType`, `userBlocks`.)
- **`ToolChoiceNone` omits `tools` entirely**, not just `tool_choice` — Chat
  Completions-style providers merely drop the tool_choice field, but this
  package skips sending `Tools`/`ToolChoice` altogether when the caller asks
  for no tools. (`providers/anthropic/wire.go:225-235`.)
- **`cache_creation_input_tokens`** (prompt-cache write count, distinct from
  `Usage.CachedInputTokens`, which is cache _reads_) surfaces under
  `Response.ProviderMetadata["anthropic"]["cache_creation_input_tokens"]`
  when non-zero, both for `Generate` (`convertResponse`,
  `providers/anthropic/wire.go:478-484`) and for streaming (`cacheCreationMetadata`,
  `providers/anthropic/language_model.go:341-354`).

## Extended thinking

Enabled per call via `ProviderOptions`, not a typed field — set
`ProviderOptions["anthropic"]["thinking"]` to `map[string]any{"type":
"enabled", "budget_tokens": N}` (same example as
[Reasoning](../core/reasoning.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("claude-sonnet-5"),
	Prompt: "What is 17 * 24? Think it through.",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{
			"thinking": map[string]any{
				"type":          "enabled",
				"budget_tokens": 2000,
			},
		},
	},
})
```

`thinking` blocks surface as `provider.ReasoningPart{Signature: ...}`;
`redacted_thinking` blocks set `Redacted: true` with the opaque payload in
`Text`. See [Reasoning](../core/reasoning.md) for the full signature
round-trip contract.

## ProviderOptions

Entries under `ProviderOptions["anthropic"]` are shallow-merged into the raw
`/v1/messages` request body verbatim — raw wire key names, no translation.
Verified directly against `providers/anthropic/provideroptions_test.go`: an
option key can override an SDK-built field (`temperature`), and a novel key
not otherwise exposed by the SDK (`top_k`) passes straight through:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("claude-sonnet-5"),
	Prompt: "Explain the Go scheduler in one sentence.",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{
			"temperature": 0.9, // overrides Call.Temperature
			"top_k":       5,   // passthrough key, not typed on Call
		},
	},
})
```

## Files and skills

`p.Files()` returns a `provider.FileStore`: `UploadFile` is a multipart
`POST {base}/v1/files` (field `file`), parsing
`{"id","filename","size_bytes","mime_type"}`; `DeleteFile` is `DELETE
{base}/v1/files/{id}`. Both send the standard `x-api-key`/
`anthropic-version` headers plus `anthropic-beta: files-api-2025-04-14` —
**this beta header is set only in `providers/anthropic/files.go`, never on
the shared `/v1/messages` path** (`providers/anthropic/language_model.go`),
verified by a dedicated test asserting the header doesn't leak onto a
chat request. Not wired into `ai.Registry`; call `.Files()` directly. See
[Media § Files and skills](../core/media.md#files-and-skills).

`p.UploadSkill(ctx, anthropic.UploadSkillCall{Zip, DisplayName})` /
`p.DeleteSkill(ctx, id)` are a **distinct, Anthropic-only** capability
(`uploadSkill` in Vercel's terms) with no generic `provider` interface —
unlike Files, which implements `provider.FileStore`. `UploadSkill`
multipart-POSTs to `{base}/v1/skills` with the file part named `files[]`
(filename `skill.zip`) and a `display_name` field, sending
`anthropic-beta: skills-2025-10-02` (isolated to `skills.go` the same way
the Files beta header is isolated to `files.go`); `DeleteSkill` is `DELETE
{base}/v1/skills/{id}` with the same beta header.

⚠ **Neither the Files nor the Skills endpoint has been verified against
the real Anthropic API** — both are implemented and tested strictly
against the documented multipart request/JSON response shapes, via an
`httptest` fixture server. Live verification should happen before relying
on either in production.

## Source of truth

- [`providers/anthropic/anthropic.go`](../../providers/anthropic/anthropic.go)
  (package doc comment, `Option`s, `defaultMaxTokens`)
- [`providers/anthropic/language_model.go`](../../providers/anthropic/language_model.go)
  (`Capabilities`, `doRequest` headers, streaming)
- [`providers/anthropic/wire.go`](../../providers/anthropic/wire.go)
  (`buildMessagesRequest`, `assistantBlocks`, `isPDFMediaType`,
  `applyProviderOptions`)
- [`providers/anthropic/provideroptions_test.go`](../../providers/anthropic/provideroptions_test.go)
- [`ai/generate_object.go`](../../ai/generate_object.go) (`buildObjectCall`,
  tool-mode fallback)
- [`providers/anthropic/files.go`](../../providers/anthropic/files.go),
  [`providers/anthropic/files_test.go`](../../providers/anthropic/files_test.go)
- [`providers/anthropic/skills.go`](../../providers/anthropic/skills.go),
  [`providers/anthropic/skills_test.go`](../../providers/anthropic/skills_test.go)

See also: [Reasoning](../core/reasoning.md) for the extended-thinking worked
example and signature round-trip rules; [Provider options](../core/provider-options.md)
for the general `ProviderOptions` merge contract.
