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
	Err       error                   // if set, every call fails with it
	Calls     []provider.Call         // records every Generate/Stream call
	Caps      provider.Capabilities
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
	return &mockStreamResponse{parts: m.Streams[idx]}, nil
}

// mockStreamResponse implements provider.StreamResponse by replaying a
// fixed slice of StreamPart values.
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
