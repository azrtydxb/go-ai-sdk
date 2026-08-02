package ai

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// closeRecordingObjModel wraps a provider.LanguageModel, returning
// StreamResponses whose Close() calls are counted, so tests can assert a
// stream's underlying connection was actually released.
type closeRecordingObjModel struct {
	inner       provider.LanguageModel
	closeCounts []*int
}

func (m *closeRecordingObjModel) ModelID() string                     { return m.inner.ModelID() }
func (m *closeRecordingObjModel) ProviderName() string                { return m.inner.ProviderName() }
func (m *closeRecordingObjModel) Capabilities() provider.Capabilities { return m.inner.Capabilities() }
func (m *closeRecordingObjModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.inner.Generate(ctx, call)
}

func (m *closeRecordingObjModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.inner.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	count := new(int)
	m.closeCounts = append(m.closeCounts, count)
	return &closeRecordingObjStream{inner: inner, count: count}, nil
}

type closeRecordingObjStream struct {
	inner provider.StreamResponse
	count *int
}

func (s *closeRecordingObjStream) Parts() iter.Seq[provider.StreamPart] { return s.inner.Parts() }
func (s *closeRecordingObjStream) Err() error                           { return s.inner.Err() }
func (s *closeRecordingObjStream) Close() error {
	*s.count++
	return s.inner.Close()
}

// TestObjectStreamCloseNeverIterated verifies that calling Close() on an
// ObjectStream whose Partials() was never ranged over still releases the
// underlying provider.StreamResponse.
func TestObjectStreamCloseNeverIterated(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"city":"Ghent","temp":21}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	rm := &closeRecordingObjModel{inner: m}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: rm, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.closeCounts) != 1 || *rm.closeCounts[0] != 0 {
		t.Fatalf("underlying stream closed before Close(): %v", rm.closeCounts)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("underlying stream close count = %d, want 1", *rm.closeCounts[0])
	}
	// Idempotent: calling Close() again must not double-close.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("underlying stream close count after second Close() = %d, want 1", *rm.closeCounts[0])
	}
}

// TestObjectStreamCloseAfterFullIterationIsNoop verifies that Close() after
// Partials() has been fully iterated (which already closes the underlying
// stream internally) does not double-close it.
func TestObjectStreamCloseAfterFullIterationIsNoop(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"city":"Ghent","temp":21}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	rm := &closeRecordingObjModel{inner: m}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: rm, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Partials() {
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("close count after full iteration = %d, want 1", *rm.closeCounts[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("close count after Close() following full iteration = %d, want 1 (no double-close)", *rm.closeCounts[0])
	}
}

func TestStreamObjectPartials(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"city":"Gh`},
			provider.TextDelta{Text: `ent","temp"`},
			provider.TextDelta{Text: `:21}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var snaps []forecast
	for p := range s.Partials() {
		snaps = append(snaps, p)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	final, err := s.Final()
	if err != nil {
		t.Fatal(err)
	}
	if final.City != "Ghent" || final.Temp != 21 {
		t.Fatalf("final = %+v", final)
	}
	if len(snaps) < 2 {
		t.Fatalf("want ≥2 distinct snapshots, got %d", len(snaps))
	}
	last := snaps[len(snaps)-1]
	if last != final {
		t.Fatalf("last snapshot %+v != final %+v", last, final)
	}
}

// TestStreamObjectToolMode verifies that in tool mode (NativeJSON=false),
// StreamObject accumulates the forced tool call's ArgsDelta stream parts
// (rather than TextDelta) to build partial and final object snapshots.
func TestStreamObjectToolMode(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Streams: [][]provider.StreamPart{{
			provider.ToolCallDelta{ID: "c1", Name: "output", ArgsDelta: `{"city":"Gh`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `ent","temp"`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `:21}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "output", Args: []byte(`{"city":"Ghent","temp":21}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		}},
	}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var snaps []forecast
	for p := range s.Partials() {
		snaps = append(snaps, p)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	final, err := s.Final()
	if err != nil {
		t.Fatal(err)
	}
	if final.City != "Ghent" || final.Temp != 21 {
		t.Fatalf("final = %+v", final)
	}
	if len(snaps) < 2 {
		t.Fatalf("want ≥2 distinct snapshots, got %d", len(snaps))
	}
	last := snaps[len(snaps)-1]
	if last != final {
		t.Fatalf("last snapshot %+v != final %+v", last, final)
	}
	call := m.Calls[0]
	if len(call.Tools) != 1 || call.ToolChoice == nil || call.ToolChoice.ToolName != "output" {
		t.Fatal("tool mode should inject schema tool with forced choice")
	}
}

// TestStreamObjectFinalNeverValid verifies that Final() returns a
// *NoObjectGeneratedError when the accumulated stream text never becomes
// valid JSON (e.g. the model refused and returned prose).
func TestStreamObjectFinalNeverValid(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "I cannot help with that."},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var snaps []forecast
	for p := range s.Partials() {
		snaps = append(snaps, p)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(snaps) != 0 {
		t.Fatalf("want 0 snapshots, got %d: %+v", len(snaps), snaps)
	}
	_, err = s.Final()
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) || noge.RawText != "I cannot help with that." {
		t.Fatalf("err = %v", err)
	}
}

// TestStreamObjectFinalAbandoned verifies that if the caller abandons
// Partials() early (breaks out of the range before the stream finishes),
// Final() reports an error rather than silently returning a zero-value T
// with a nil error — which would look like a successful decode of an empty
// object.
func TestStreamObjectFinalAbandoned(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"city":"Gh`},
			provider.TextDelta{Text: `ent","temp"`},
			provider.TextDelta{Text: `:21}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Partials() {
		break // abandon after the first partial
	}

	final, err := s.Final()
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) {
		t.Fatalf("want *NoObjectGeneratedError, got final=%+v err=%v", final, err)
	}
}

// TestStreamObjectFinalNeverStarted verifies that calling Final() before
// Partials() has ever been ranged over reports an error rather than
// silently returning a zero-value T with a nil error — the same
// false-success class as abandoning mid-iteration.
func TestStreamObjectFinalNeverStarted(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"city":"Ghent","temp":21}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	s, err := StreamObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}

	final, err := s.Final()
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) {
		t.Fatalf("want *NoObjectGeneratedError, got final=%+v err=%v", final, err)
	}
}
