// Package aitest provides test doubles for the ai and provider packages.
package aitest

import (
	"context"
	"errors"
	"iter"
	"sync"

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
	Err        error // if set, every call fails with it
	// Calls records every Generate/Stream call. Direct access is safe only
	// from a single goroutine (e.g. after all calls have completed); if
	// Generate/Stream may be called concurrently, read via RecordedCalls
	// instead, which takes a locked snapshot.
	Calls []provider.Call
	Caps  provider.Capabilities

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking Generate/Stream on m.
func (m *MockModel) RecordedCalls() []provider.Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.Call(nil), m.Calls...)
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
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	idx := len(m.Calls) - 1
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	if idx >= len(m.Responses) {
		panic("aitest.MockModel: Responses exhausted")
	}
	return m.Responses[idx], nil
}

// Stream implements provider.LanguageModel. It records the call, then
// returns Err if set, otherwise a StreamResponse replaying
// Streams[len(Calls)-1]. Panics if the Streams slice is exhausted.
func (m *MockModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	idx := len(m.Calls) - 1
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
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
	BatchSize int // MaxBatchSize(); default 2 if zero
	// Batches records each Embed call's values. Direct access is safe only
	// from a single goroutine; if Embed may be called concurrently, read via
	// RecordedBatches instead, which takes a locked snapshot.
	Batches [][]string
	Dim     int   // embedding size; default 3
	Err     error // if set, every call fails with it

	mu sync.Mutex
}

// RecordedBatches returns a locked copy of Batches, safe to call while other
// goroutines are concurrently invoking Embed on m.
func (m *MockEmbedder) RecordedBatches() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]string(nil), m.Batches...)
}

// Embed implements provider.EmbeddingModel. It records the values in Batches,
// then returns Err if set, otherwise deterministic vectors where each
// component of vector i equals float64(len(values[i])). Usage.TotalTokens
// equals len(values).
func (m *MockEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	m.mu.Lock()
	m.Batches = append(m.Batches, values)
	m.mu.Unlock()
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
	Err      error // if set, every call fails with it
	// Calls records every GenerateImages call. Direct access is safe only
	// from a single goroutine; if GenerateImages may be called concurrently,
	// read via RecordedCalls instead, which takes a locked snapshot.
	Calls []provider.ImageCall

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking GenerateImages on m.
func (m *MockImageModel) RecordedCalls() []provider.ImageCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.ImageCall(nil), m.Calls...)
}

// GenerateImages implements provider.ImageModel. It records the call, then
// returns Err if set, otherwise Response.
func (m *MockImageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.ImageModel.
func (m *MockImageModel) ModelID() string { return "mock" }

// ProviderName implements provider.ImageModel.
func (m *MockImageModel) ProviderName() string { return "aitest" }

// MockVideoModel is a provider.VideoModel test double that returns a
// scripted response or fails every call with Err if set.
type MockVideoModel struct {
	Response *provider.VideoResponse
	Err      error // if set, every call fails with it
	// Calls records every GenerateVideos call. Direct access is safe only
	// from a single goroutine; if GenerateVideos may be called concurrently,
	// read via RecordedCalls instead, which takes a locked snapshot.
	Calls []provider.VideoCall

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking GenerateVideos on m.
func (m *MockVideoModel) RecordedCalls() []provider.VideoCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.VideoCall(nil), m.Calls...)
}

// GenerateVideos implements provider.VideoModel. It records the call, then
// returns Err if set, otherwise Response.
func (m *MockVideoModel) GenerateVideos(ctx context.Context, call provider.VideoCall) (*provider.VideoResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.VideoModel.
func (m *MockVideoModel) ModelID() string { return "mock" }

// ProviderName implements provider.VideoModel.
func (m *MockVideoModel) ProviderName() string { return "aitest" }

// MockSpeechModel is a provider.SpeechModel test double that returns a
// scripted response or fails every call with Err if set.
type MockSpeechModel struct {
	Response *provider.SpeechResponse
	Err      error // if set, every call fails with it
	// Calls records every GenerateSpeech call. Direct access is safe only
	// from a single goroutine; if GenerateSpeech may be called concurrently,
	// read via RecordedCalls instead, which takes a locked snapshot.
	Calls []provider.SpeechCall

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking GenerateSpeech on m.
func (m *MockSpeechModel) RecordedCalls() []provider.SpeechCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.SpeechCall(nil), m.Calls...)
}

// GenerateSpeech implements provider.SpeechModel. It records the call, then
// returns Err if set, otherwise Response.
func (m *MockSpeechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
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
	Err      error // if set, every call fails with it
	// Calls records every Transcribe call. Direct access is safe only from a
	// single goroutine; if Transcribe may be called concurrently, read via
	// RecordedCalls instead, which takes a locked snapshot.
	Calls []provider.TranscriptionCall

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking Transcribe on m.
func (m *MockTranscriptionModel) RecordedCalls() []provider.TranscriptionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.TranscriptionCall(nil), m.Calls...)
}

// Transcribe implements provider.TranscriptionModel. It records the call,
// then returns Err if set, otherwise Response.
func (m *MockTranscriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.TranscriptionModel.
func (m *MockTranscriptionModel) ModelID() string { return "mock" }

// ProviderName implements provider.TranscriptionModel.
func (m *MockTranscriptionModel) ProviderName() string { return "aitest" }

// MockTranslationModel is a provider.TranslationModel test double that
// returns a scripted response or fails every call with Err if set.
type MockTranslationModel struct {
	Response *provider.TranslationResponse
	Err      error // if set, every call fails with it
	// Calls records every Translate call. Direct access is safe only from a
	// single goroutine; if Translate may be called concurrently, read via
	// RecordedCalls instead, which takes a locked snapshot.
	Calls []provider.TranslationCall

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking Translate on m.
func (m *MockTranslationModel) RecordedCalls() []provider.TranslationCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.TranslationCall(nil), m.Calls...)
}

// Translate implements provider.TranslationModel. It records the call,
// then returns Err if set, otherwise Response.
func (m *MockTranslationModel) Translate(ctx context.Context, call provider.TranslationCall) (*provider.TranslationResponse, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Response, nil
}

// ModelID implements provider.TranslationModel.
func (m *MockTranslationModel) ModelID() string { return "mock" }

// ProviderName implements provider.TranslationModel.
func (m *MockTranslationModel) ProviderName() string { return "aitest" }

// MockFileStore is a provider.FileStore test double that returns a scripted
// response for UploadFile, or fails with UploadErr/DeleteErr if set.
type MockFileStore struct {
	UploadResponse *provider.FileInfo
	UploadErr      error // if set, every UploadFile call fails with it
	DeleteErr      error // if set, every DeleteFile call fails with it
	// UploadCalls records every UploadFile call, and DeleteCalls every
	// DeleteFile call's id. Direct access is safe only from a single
	// goroutine; if UploadFile/DeleteFile may be called concurrently, read
	// via RecordedUploadCalls/RecordedDeleteCalls instead, which take locked
	// snapshots.
	UploadCalls []provider.FileUploadCall
	DeleteCalls []string

	mu sync.Mutex
}

// RecordedUploadCalls returns a locked copy of UploadCalls, safe to call
// while other goroutines are concurrently invoking UploadFile on m.
func (m *MockFileStore) RecordedUploadCalls() []provider.FileUploadCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.FileUploadCall(nil), m.UploadCalls...)
}

// RecordedDeleteCalls returns a locked copy of DeleteCalls, safe to call
// while other goroutines are concurrently invoking DeleteFile on m.
func (m *MockFileStore) RecordedDeleteCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.DeleteCalls...)
}

// UploadFile implements provider.FileStore. It records the call, then
// returns UploadErr if set, otherwise UploadResponse.
func (m *MockFileStore) UploadFile(ctx context.Context, call provider.FileUploadCall) (*provider.FileInfo, error) {
	m.mu.Lock()
	m.UploadCalls = append(m.UploadCalls, call)
	m.mu.Unlock()
	if m.UploadErr != nil {
		return nil, m.UploadErr
	}
	return m.UploadResponse, nil
}

// DeleteFile implements provider.FileStore. It records the call, then
// returns DeleteErr if set, otherwise nil.
func (m *MockFileStore) DeleteFile(ctx context.Context, id string) error {
	m.mu.Lock()
	m.DeleteCalls = append(m.DeleteCalls, id)
	m.mu.Unlock()
	return m.DeleteErr
}

// ProviderName implements provider.FileStore.
func (m *MockFileStore) ProviderName() string { return "aitest" }

// MockStreamingTranscriptionModel is a provider.StreamingTranscriptionModel
// test double that replays a scripted event sequence, or fails every call
// with Err if set.
type MockStreamingTranscriptionModel struct {
	Events []provider.TranscriptEvent // replayed in order by the returned stream
	// StreamErr, if set, is what the returned stream's Err() reports once
	// Events has been fully replayed, simulating a mid-stream failure.
	StreamErr error
	Err       error // if set, StreamTranscribe itself fails with it
	// Calls records every StreamTranscribe call. Sent records every Send
	// call's audio, across all streams. CloseSent counts CloseSend calls,
	// across all streams. Direct access to these fields is safe only from a
	// single goroutine; if StreamTranscribe or the streams it returns may be
	// used concurrently, read via RecordedCalls/RecordedSent/
	// RecordedCloseSentCount instead, which take locked snapshots.
	Calls     []provider.StreamTranscriptionCall
	Sent      [][]byte
	CloseSent int

	mu sync.Mutex
}

// RecordedCalls returns a locked copy of Calls, safe to call while other
// goroutines are concurrently invoking StreamTranscribe on m.
func (m *MockStreamingTranscriptionModel) RecordedCalls() []provider.StreamTranscriptionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.StreamTranscriptionCall(nil), m.Calls...)
}

// RecordedSent returns a locked copy of Sent, safe to call while other
// goroutines are concurrently calling Send on streams returned by m.
func (m *MockStreamingTranscriptionModel) RecordedSent() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]byte(nil), m.Sent...)
}

// RecordedCloseSentCount returns a locked snapshot of CloseSent, safe to
// call while other goroutines are concurrently calling CloseSend on streams
// returned by m.
func (m *MockStreamingTranscriptionModel) RecordedCloseSentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CloseSent
}

// StreamTranscribe implements provider.StreamingTranscriptionModel. It
// records the call, then returns Err if set, otherwise a
// *MockTranscriptionStream replaying Events.
func (m *MockStreamingTranscriptionModel) StreamTranscribe(ctx context.Context, call provider.StreamTranscriptionCall) (provider.TranscriptionStream, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, call)
	m.mu.Unlock()
	if m.Err != nil {
		return nil, m.Err
	}
	return &MockTranscriptionStream{model: m, events: m.Events, err: m.StreamErr}, nil
}

// ModelID implements provider.StreamingTranscriptionModel.
func (m *MockStreamingTranscriptionModel) ModelID() string { return "mock" }

// ProviderName implements provider.StreamingTranscriptionModel.
func (m *MockStreamingTranscriptionModel) ProviderName() string { return "aitest" }

// MockTranscriptionStream implements provider.TranscriptionStream by
// replaying a fixed slice of TranscriptEvent values, then reporting err (if
// any, scripted via MockStreamingTranscriptionModel.StreamErr) from Err()
// to simulate a mid-stream failure.
type MockTranscriptionStream struct {
	model  *MockStreamingTranscriptionModel
	events []provider.TranscriptEvent
	err    error

	mu     sync.Mutex
	closed bool
	ended  bool
}

// Send implements provider.TranscriptionStream. It records the audio on the
// owning model.
func (s *MockTranscriptionStream) Send(ctx context.Context, audio []byte) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("aitest: Send after Close")
	}
	s.model.mu.Lock()
	s.model.Sent = append(s.model.Sent, audio)
	s.model.mu.Unlock()
	return nil
}

// CloseSend implements provider.TranscriptionStream. Idempotent.
func (s *MockTranscriptionStream) CloseSend(ctx context.Context) error {
	s.model.mu.Lock()
	s.model.CloseSent++
	s.model.mu.Unlock()
	return nil
}

// Events implements provider.TranscriptionStream. Single use.
func (s *MockTranscriptionStream) Events() iter.Seq[provider.TranscriptEvent] {
	return func(yield func(provider.TranscriptEvent) bool) {
		defer func() {
			s.mu.Lock()
			s.ended = true
			s.mu.Unlock()
		}()
		for _, e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

// Err implements provider.TranscriptionStream.
func (s *MockTranscriptionStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close implements provider.TranscriptionStream. Idempotent.
func (s *MockTranscriptionStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
