// Package aitest provides test doubles for the ai and provider packages.
package aitest

import (
	"context"
	"iter"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// MockModel is a provider.LanguageModel test double that replays canned
// responses/streams in order, or fails every call with Err if set.
type MockModel struct {
	Responses []*provider.Response    // returned in order by Generate
	Streams   [][]provider.StreamPart // returned in order by Stream
	// StreamErrs, if set, is consulted in parallel with Streams: after the
	// stream at Streams[i] finishes replaying its parts, the returned
	// StreamResponse's Err() reports StreamErrs[i] (if non-nil and index in
	// range) instead of nil, simulating a mid-stream failure — e.g. the
	// connection dropping after some deltas arrived but before a FinishPart.
	StreamErrs []error
	Err        error           // if set, every call fails with it
	Calls      []provider.Call // records every Generate/Stream call
	Caps       provider.Capabilities
}

// ModelID implements provider.LanguageModel.
func (m *MockModel) ModelID() string { return "mock" }

// ProviderName implements provider.LanguageModel.
func (m *MockModel) ProviderName() string { return "aitest" }

// Capabilities implements provider.LanguageModel.
func (m *MockModel) Capabilities() provider.Capabilities { return m.Caps }

// Generate implements provider.LanguageModel. It records the call, then
// returns Err if set, otherwise Responses[len(Calls)-1]. Panics if the
// Responses slice is exhausted.
func (m *MockModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil {
		return nil, m.Err
	}
	idx := len(m.Calls) - 1
	if idx >= len(m.Responses) {
		panic("aitest.MockModel: Responses exhausted")
	}
	return m.Responses[idx], nil
}

// Stream implements provider.LanguageModel. It records the call, then
// returns Err if set, otherwise a StreamResponse replaying
// Streams[len(Calls)-1]. Panics if the Streams slice is exhausted.
func (m *MockModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil {
		return nil, m.Err
	}
	idx := len(m.Calls) - 1
	if idx >= len(m.Streams) {
		panic("aitest.MockModel: Streams exhausted")
	}
	var streamErr error
	if idx < len(m.StreamErrs) {
		streamErr = m.StreamErrs[idx]
	}
	return &mockStreamResponse{parts: m.Streams[idx], err: streamErr}, nil
}

// mockStreamResponse implements provider.StreamResponse by replaying a
// fixed slice of StreamPart values, then reporting err (if any, scripted via
// MockModel.StreamErrs) from Err() to simulate a mid-stream failure.
type mockStreamResponse struct {
	parts  []provider.StreamPart
	err    error
	closed bool
}

// Parts implements provider.StreamResponse.
func (s *mockStreamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for _, p := range s.parts {
			if !yield(p) {
				return
			}
		}
	}
}

// Err implements provider.StreamResponse.
func (s *mockStreamResponse) Err() error { return s.err }

// Close implements provider.StreamResponse. Safe to call twice.
func (s *mockStreamResponse) Close() error {
	s.closed = true
	return nil
}

// MockEmbedder is a provider.EmbeddingModel test double that returns
// deterministic vectors or fails every call with Err if set.
type MockEmbedder struct {
	BatchSize int        // MaxBatchSize(); default 2 if zero
	Batches   [][]string // records each Embed call's values
	Dim       int        // embedding size; default 3
	Err       error      // if set, every call fails with it
}

// Embed implements provider.EmbeddingModel. It records the values in Batches,
// then returns Err if set, otherwise deterministic vectors where each
// component of vector i equals float64(len(values[i])). Usage.TotalTokens
// equals len(values).
func (m *MockEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	m.Batches = append(m.Batches, values)
	if m.Err != nil {
		return nil, m.Err
	}

	dim := m.Dim
	if dim == 0 {
		dim = 3
	}

	embeddings := make([][]float64, len(values))
	for i, val := range values {
		emb := make([]float64, dim)
		for j := range emb {
			emb[j] = float64(len(val))
		}
		embeddings[i] = emb
	}

	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage:      provider.Usage{TotalTokens: len(values)},
	}, nil
}

// MaxBatchSize implements provider.EmbeddingModel.
func (m *MockEmbedder) MaxBatchSize() int {
	if m.BatchSize == 0 {
		return 2
	}
	return m.BatchSize
}

// ModelID implements provider.EmbeddingModel.
func (m *MockEmbedder) ModelID() string { return "mock-embedder" }

// ProviderName implements provider.EmbeddingModel.
func (m *MockEmbedder) ProviderName() string { return "aitest" }

// MockImageModel is a provider.ImageModel test double that returns a
// scripted response or fails every call with Err if set.
type MockImageModel struct {
	Response *provider.ImageResponse
	Err      error                // if set, every call fails with it
	Calls    []provider.ImageCall // records every GenerateImages call
}

// GenerateImages implements provider.ImageModel. It records the call, then
// returns Err if set, otherwise Response.
func (m *MockImageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.ImageModel.
func (m *MockImageModel) ModelID() string { return "mock" }

// ProviderName implements provider.ImageModel.
func (m *MockImageModel) ProviderName() string { return "aitest" }

// MockSpeechModel is a provider.SpeechModel test double that returns a
// scripted response or fails every call with Err if set.
type MockSpeechModel struct {
	Response *provider.SpeechResponse
	Err      error                 // if set, every call fails with it
	Calls    []provider.SpeechCall // records every GenerateSpeech call
}

// GenerateSpeech implements provider.SpeechModel. It records the call, then
// returns Err if set, otherwise Response.
func (m *MockSpeechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.SpeechModel.
func (m *MockSpeechModel) ModelID() string { return "mock" }

// ProviderName implements provider.SpeechModel.
func (m *MockSpeechModel) ProviderName() string { return "aitest" }

// MockTranscriptionModel is a provider.TranscriptionModel test double that
// returns a scripted response or fails every call with Err if set.
type MockTranscriptionModel struct {
	Response *provider.TranscriptionResponse
	Err      error                        // if set, every call fails with it
	Calls    []provider.TranscriptionCall // records every Transcribe call
}

// Transcribe implements provider.TranscriptionModel. It records the call,
// then returns Err if set, otherwise Response.
func (m *MockTranscriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.TranscriptionModel.
func (m *MockTranscriptionModel) ModelID() string { return "mock" }

// ProviderName implements provider.TranscriptionModel.
func (m *MockTranscriptionModel) ProviderName() string { return "aitest" }
