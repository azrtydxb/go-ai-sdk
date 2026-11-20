# Logprobs

`GenerateTextOpts.Logprobs` (and `provider.Call.Logprobs`) asks the model for
the log probability of each generated token, optionally with its most likely
alternatives. It is how you read a model's confidence, for instance to score
a self-hosted classifier served as a chat model.

```go
top := 5
res, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:       model,
	Prompt:      "Answer yes or no: is the sky blue?",
	MaxTokens:   ptr(1),
	Logprobs:    true,
	TopLogprobs: &top,
})
for _, tok := range res.Logprobs {
	fmt.Printf("%q %.3f\n", tok.Token, tok.Logprob)
	for _, alt := range tok.TopLogprobs {
		fmt.Printf("  %q %.3f\n", alt.Token, alt.Logprob)
	}
}
```

- **`Logprobs`** (`bool`) — request per-token log probabilities.
- **`TopLogprobs`** (`*int`) — also request that many alternatives per token
  (0 to 20 on OpenAI and Gemini). It requires `Logprobs`; a call with
  `TopLogprobs` and no `Logprobs` is rejected before anything is sent.

The result is a `[]provider.TokenLogprob`, in generation order, on
`provider.Response.Logprobs`, `ai.GenerateTextResult.Logprobs` (the last
step's) and `ai.Step.Logprobs`. Each entry has `Token`, `Logprob` (natural
log), `Bytes` (the token's UTF-8 bytes where the provider reports them) and
`TopLogprobs` (`[]provider.TopLogprob`, most likely first; it may include the
chosen token).

## Streaming

A stream carries them as `provider.LogprobsDelta` parts next to the text
deltas of the same chunk. Concatenating every delta's `Tokens` gives the
non-streaming list. `ai.StreamText` accumulates them per step:
`TextStream.Logprobs()` returns the final step's, and the same list lands in
the finished `GenerateTextResult`.

## Providers

| Provider                                               | Support                                                                                                                                                                                                     |
| ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| OpenAI-compatible (OpenAI, Azure, vLLM, DeepSeek, ...) | `logprobs` / `top_logprobs`, response `choices[].logprobs.content[]` (also per streamed chunk). Whether a given server honors it is the server's call; one that refuses answers with an `*ai.APICallError`. |
| Google, Vertex                                         | `generationConfig.responseLogprobs` / `logprobs`, response `candidates[].logprobsResult` (the chosen token and the ranked alternatives per step). No `Bytes`.                                               |
| Anthropic, Bedrock, Cohere                             | not supported: `*provider.UnsupportedFeatureError` instead of silently returning no log probabilities                                                                                                       |

Logprobs are passed through the middleware that rewrites responses
(`ai.ExtractReasoningMiddleware`, the JSON-fence extractor,
`ai.SimulateStreamingMiddleware`).
