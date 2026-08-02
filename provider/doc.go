// Package provider is the spec every go-ai-sdk provider implements: a set
// of interfaces (LanguageModel, EmbeddingModel, ImageModel, SpeechModel,
// TranscriptionModel) plus the unified request/response types they share —
// Call, Response, Message/ContentPart, StreamPart, ToolDef, FinishReason,
// Usage. It contains no orchestration logic and no vendor-specific code:
// package ai builds the high-level API on top of these interfaces, and
// each package under providers/ implements them against one vendor's wire
// format.
//
// A LanguageModel is usable directly, without going through package ai:
//
//	var model provider.LanguageModel = anthropic.New().Model("claude-sonnet-5")
//
//	resp, err := model.Generate(ctx, provider.Call{
//		Messages: []provider.Message{provider.UserText("Say hello.")},
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(resp.Text())
//
// Streaming implementations (StreamResponse, returned from
// LanguageModel.Stream) follow a small set of disciplines documented in
// full at docs/architecture.md: Parts() is single-use, yields track the
// underlying wire's events (not a fixed schedule), a well-formed stream
// yields exactly one FinishPart and an errored one yields none, and Close
// is idempotent but not safe for concurrent use alongside an in-progress
// Parts() iteration.
//
// Subpackage providertest is a black-box conformance suite every
// LanguageModel implementation should pass; see its package doc for the
// scenario matrix.
package provider
