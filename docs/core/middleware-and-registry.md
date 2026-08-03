# Middleware and registry

`go-ai-sdk` middlewares are plain `provider.LanguageModel` decorators —
functions that take a model and return a wrapped model implementing the
same interface. `ai.WrapModel` is a one-line hook for applying them; four
middlewares ship with the SDK. `ai.WrapImageModel` is the equivalent hook
for `provider.ImageModel`. `ai.Registry` maps provider names to provider
values and resolves `"provider:model"` IDs into concrete models.

## WrapModel

```go
func WrapModel(m provider.LanguageModel, wrap func(provider.LanguageModel) provider.LanguageModel) provider.LanguageModel {
	return wrap(m)
}
```

It exists purely for readability at the call site — `wrap(m)` alone does
the same thing — as a named hook for middleware that decorates a model
(logging, caching, retries, or any of the three below) before passing it to
`GenerateText`/`StreamText`.

## The four middlewares

### ExtractReasoningMiddleware

```go
func ExtractReasoningMiddleware(model provider.LanguageModel, opts ExtractReasoningOpts) provider.LanguageModel

type ExtractReasoningOpts struct {
	TagName            string // required, e.g. "think"
	StartWithReasoning bool
}
```

Pulls `<tagName>...</tagName>` spans out of a model's text output and
re-emits them as reasoning content. See [Reasoning](reasoning.md#extractreasoningmiddleware)
for the full behavior, both `StartWithReasoning` modes, and the incremental
streaming guarantees.

### SimulateStreamingMiddleware

```go
func SimulateStreamingMiddleware(model provider.LanguageModel) provider.LanguageModel
```

Makes `model.Stream` call `Generate` instead, then replay the resulting
`Response` as a synthetic single-chunk stream: a `ReasoningDelta` +
`ReasoningEnd` pair for each `ReasoningPart` (in order), then a `TextDelta`
for each `TextPart`, then a `ToolCallEnd` for each tool call, then one
`FinishPart` carrying the response's `FinishReason`/`Usage`. This lets code
written against the streaming API work uniformly with models/providers that
only support non-streaming `Generate`. It takes no options.

### DefaultSettingsMiddleware

```go
func DefaultSettingsMiddleware(model provider.LanguageModel, defaults provider.Call) provider.LanguageModel
```

Fills in a call's zero-valued fields from `defaults` before it reaches
`model`: `Temperature`/`TopP`/`MaxTokens`/`TopK`/`PresencePenalty`/
`FrequencyPenalty`/`Seed`/`Reasoning` (nil pointers — `Reasoning` fills as a
whole `*ReasoningConfig` pointer, not merged field-by-field within it),
`StopSequences` (empty slice), `Headers` (merged per header key, with
per-call keys winning over the matching default key), and `ProviderOptions`
(merged per provider-name namespace, with per-call entries winning over the
matching default entries within a namespace — see
[Provider options](provider-options.md) for the namespace convention).
Every other `Call` field (`Messages`, `Tools`, `ToolChoice`,
`ResponseFormat`) passes through unmodified. Per-call values always win —
zero-valued/unset fields are replaced outright, while `Headers` and
`ProviderOptions` are merged key-by-key/namespace-by-namespace with the
per-call side winning on conflicts.

### ExtractJSONMiddleware

```go
func ExtractJSONMiddleware(model provider.LanguageModel) provider.LanguageModel
```

Strips markdown code fences from a model's text output — a common way
models wrap JSON they were asked to produce "raw" (e.g.
` ```json\n{...}\n``` `).

```go
model := ai.ExtractJSONMiddleware(openai.New().Model("gpt-4o"))

result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Return the user's profile as raw JSON, no commentary.",
})
// result.Text has any ```json fence markers already stripped.
```

`Generate` reuses `GenerateObject`'s non-native-JSON decoding rule exactly
(`stripFences`): the response text is trimmed, and a leading ` ``` ` (or
` ```json `) plus a trailing ` ``` ` are removed together — only when
**both** are present at their respective ends of the trimmed text. Text
that isn't fenced at both ends passes through unchanged, including text
with fence lines embedded in the middle of otherwise-unfenced prose.

`Stream` strips fences incrementally, mirroring that same leading/trailing
rule as closely as streaming allows:

- An opening fence line is stripped only when it is the **first non-empty
  line of the stream** — decided (and emitted) immediately, no buffering
  needed.
- A closing fence line is stripped only when it **terminates the
  stream** — nothing but whitespace follows it before the stream ends.
  Since streaming can't know "nothing follows" until the stream actually
  ends, a candidate closing-fence line (plus any trailing whitespace) is
  buffered rather than emitted immediately; if non-whitespace content
  later arrives, the buffered candidate is flushed verbatim as ordinary
  text (it wasn't terminal after all).
- Any other ` ``` `-prefixed line — neither the first non-empty line nor
  ultimately terminal — passes through unchanged, same as prose-embedded
  fences under `Generate`'s rule.

At most one candidate closing-fence line (plus whatever whitespace follows
it, until resolved) is ever buffered; a fence marker split across two
deltas (e.g. two backticks then "`json\n") is still recognized correctly,
since the relevant undecided prefix carries over between feeds.

**Divergence from `Generate`'s rule:** `Generate` requires *both* a leading
and a trailing fence before stripping either — a leading fence alone is
left untouched, since the text might not actually be fenced. `Stream`
cannot wait indefinitely to find out whether a closing fence will ever
arrive, so it strips a resolved opening fence unconditionally; a stream
that's truncated before any closing fence appears ends up with its opening
fence stripped and nothing left to strip for the (nonexistent) close.

**Fence-scanner caveat:** the scanner only recognizes a fence by its first
three bytes (` ``` `) at the start of a line. Text that legitimately starts
a line with three literal backticks for a reason *other than* a markdown
code fence (rare, but possible in a model's raw text output) will still be
treated as a fence marker and stripped — there's no semantic check that the
surrounding content is actually JSON before removing the fence lines.

### WrapImageModel

```go
func WrapImageModel(m provider.ImageModel, wrap func(provider.ImageModel) provider.ImageModel) provider.ImageModel {
	return wrap(m)
}
```

The `provider.ImageModel` counterpart to `WrapModel` — a one-line naming
hook for middleware that decorates an image model (logging, caching,
provider-option injection) before it's passed to `ai.GenerateImage`. It's a
naming hook only: no built-in image middlewares ship with the SDK yet
(unlike the four `provider.LanguageModel` middlewares above), so `wrap` is
always a function you write yourself, e.g.:

```go
model := ai.WrapImageModel(openai.New().ImageModel("gpt-image-1"),
	func(m provider.ImageModel) provider.ImageModel {
		return &loggingImageModel{inner: m}
	})
```

## Composition order

Since each middleware wraps the model it's given, the **outermost**
`WrapModel`/manual wrap call is the first thing a `Generate`/`Stream` call
passes through, and the **innermost** (closest to the real model) is the
last thing before the actual provider call:

```go
model := ai.WrapModel(baseModel, func(m provider.LanguageModel) provider.LanguageModel {
	m = ai.ExtractReasoningMiddleware(m, ai.ExtractReasoningOpts{TagName: "think"})
	m = ai.DefaultSettingsMiddleware(m, provider.Call{Temperature: ptr(0.7)})
	return m
})
```

Here `DefaultSettingsMiddleware` is outermost (assigned last), so it fills
in defaults on the `Call` *before* `ExtractReasoningMiddleware` sees it,
which in turn passes the call straight through to `baseModel` and then
post-processes `baseModel`'s response/stream to extract reasoning. In
general: put settings/defaults middleware outermost so it shapes every
call before anything else runs; put response-shaping middleware
(`ExtractReasoningMiddleware`, `SimulateStreamingMiddleware`) closest to the
real model, since they interpret that specific model's raw output format.

## Registry

```go
type Registry struct { /* ... */ }

func NewRegistry() *Registry
func (r *Registry) Register(name string, p any)

func (r *Registry) LanguageModel(id string) (provider.LanguageModel, error)
func (r *Registry) EmbeddingModel(id string) (provider.EmbeddingModel, error)
func (r *Registry) ImageModel(id string) (provider.ImageModel, error)
func (r *Registry) SpeechModel(id string) (provider.SpeechModel, error)
func (r *Registry) TranscriptionModel(id string) (provider.TranscriptionModel, error)
func (r *Registry) RerankingModel(id string) (provider.RerankingModel, error)
```

`Register` stores `p` (typically a provider package's `*Provider` value,
e.g. `openai.New()`) under `name`. `p`'s capabilities — which of
`LanguageModelProvider`, `EmbeddingModelProvider`, `ImageModelProvider`,
`SpeechModelProvider`, `TranscriptionModelProvider`, `RerankingModelProvider`
it implements — are checked lazily at lookup time via a type assertion, so
`p` need not implement every capability interface (Bedrock, for instance,
has no `ImageModel` method; only Cohere implements
`RerankingModelProvider` this wave — see
[Embeddings § Reranking](embeddings.md#reranking)).

```go
reg := ai.NewRegistry()
reg.Register("openai", openai.New())
reg.Register("bedrock", bedrock.New())

model, err := reg.LanguageModel("openai:gpt-4o")
em, err := reg.EmbeddingModel("openai:text-embedding-3-small")
```

Each of the six lookups splits `id` on the first `:`, resolves the
provider by name, then type-asserts it against the matching
`*ModelProvider` interface — returning an error rather than panicking for
an unknown provider name, a malformed id (no `:` at all), or a provider
that doesn't support the requested capability:

```go
_, err := reg.LanguageModel("nope:model")
// ai: unknown provider "nope"

_, err = reg.LanguageModel("no-colon-here")
// ai: invalid model id "no-colon-here" (want "provider:model")

_, err = reg.ImageModel("bedrock:some-model")
// ai: provider "bedrock" does not support image models
```

### The colon-safe split rule

Splitting happens on the **first** `:` only (`strings.Cut`, not
`strings.Split`), so a model ID that itself contains a colon — e.g.
Bedrock's `"anthropic.claude-3:1"` — round-trips intact:

```go
reg.Register("bedrock", bedrock.New())
m, _ := reg.LanguageModel("bedrock:anthropic.claude-3:1")
m.ModelID() // "anthropic.claude-3:1"
```

`Registry` is safe for concurrent `Register`/lookup calls (guarded by an
internal `sync.RWMutex`).

## Source of truth

- [`ai/generate_text.go`](../../ai/generate_text.go) (`WrapModel`, `WrapImageModel`)
- [`ai/middleware.go`](../../ai/middleware.go)
- [`ai/middleware_json.go`](../../ai/middleware_json.go) (`ExtractJSONMiddleware`)
- [`ai/registry.go`](../../ai/registry.go)

See also: [Provider options](provider-options.md) for the
`ProviderOptions`/`ProviderMetadata` conventions `DefaultSettingsMiddleware`
merges; [Reasoning](reasoning.md) for `ExtractReasoningMiddleware`'s full
behavior.
