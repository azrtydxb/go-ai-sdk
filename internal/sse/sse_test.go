package sse

import (
	"errors"
	"strings"
	"testing"
)

func collect(t *testing.T, input string) []Event {
	t.Helper()
	var out []Event
	for ev, err := range Scan(strings.NewReader(input)) {
		if err != nil {
			t.Fatalf("scan error: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

func TestScanBasicEvents(t *testing.T) {
	evs := collect(t, "data: one\n\ndata: two\n\n")
	if len(evs) != 2 || evs[0].Data != "one" || evs[1].Data != "two" {
		t.Fatalf("evs = %#v", evs)
	}
}

func TestScanNamedEventAndMultilineData(t *testing.T) {
	evs := collect(t, "event: delta\ndata: a\ndata: b\n\n")
	if len(evs) != 1 || evs[0].Event != "delta" || evs[0].Data != "a\nb" {
		t.Fatalf("evs = %#v", evs)
	}
}

func TestScanSkipsComments(t *testing.T) {
	evs := collect(t, ": keep-alive\n\ndata: x\n\n")
	if len(evs) != 1 || evs[0].Data != "x" {
		t.Fatalf("evs = %#v", evs)
	}
}

func TestScanNoSpaceAfterColon(t *testing.T) {
	evs := collect(t, "data:x\n\n")
	if len(evs) != 1 || evs[0].Data != "x" {
		t.Fatalf("evs = %#v", evs)
	}
}

func TestScanPartialEventWithReadError(t *testing.T) {
	// Test that a consumer breaking on first non-nil error doesn't cause panic.
	// Simulates a reader that returns partial data then a non-EOF error.
	errTest := errors.New("test error")
	src := &errorReader{
		data: "data: partial\n",
		err:  errTest,
	}

	var eventCount int
	for _, err := range Scan(src) {
		eventCount++
		if err != nil {
			if err != errTest {
				t.Fatalf("unexpected error: %v", err)
			}
			// Consumer breaks on first non-nil error (the idiomatic pattern).
			break
		}
	}

	// Only the error should be yielded; partial event is not dispatched.
	if eventCount != 1 {
		t.Fatalf("expected 1 yield (the error), got %d", eventCount)
	}
}

func TestScanTrailingUnterminated(t *testing.T) {
	// Per SSE spec, events are only dispatched on blank line.
	// A trailing unterminated event at clean EOF should not be dispatched.
	evs := collect(t, "data: trailing")
	if len(evs) != 0 {
		t.Fatalf("trailing unterminated event should not be dispatched, got %#v", evs)
	}
}

// errorReader returns data, then err on subsequent reads.
type errorReader struct {
	data string
	err  error
	pos  int
}

func (r *errorReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestScanEventExceedingMaxEventBytesYieldsError(t *testing.T) {
	orig := MaxEventBytes
	MaxEventBytes = 1024
	defer func() { MaxEventBytes = orig }()

	// Many `data:` lines that never terminate with a blank line: without the
	// cap this accumulates unbounded. With the cap, Scan must yield an error
	// once accumulated data exceeds MaxEventBytes and stop.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("data: ")
		b.WriteString(strings.Repeat("x", 64))
		b.WriteString("\n")
	}
	var gotErr error
	var events int
	for ev, err := range Scan(strings.NewReader(b.String())) {
		if err != nil {
			gotErr = err
			break
		}
		_ = ev
		events++
	}
	if gotErr == nil {
		t.Fatal("expected an error once accumulated data exceeds MaxEventBytes, got nil")
	}
	if !strings.Contains(gotErr.Error(), "MaxEventBytes") {
		t.Fatalf("error = %v, want it to mention MaxEventBytes", gotErr)
	}
	if events != 0 {
		t.Fatalf("no complete event should have dispatched, got %d", events)
	}
}

func TestScanUnderMaxEventBytesStillAssembles(t *testing.T) {
	orig := MaxEventBytes
	MaxEventBytes = 1 << 20
	defer func() { MaxEventBytes = orig }()
	// A large-but-under-ceiling multi-line event still assembles correctly.
	evs := collect(t, "data: a\ndata: b\ndata: c\n\n")
	if len(evs) != 1 || evs[0].Data != "a\nb\nc" {
		t.Fatalf("evs = %#v", evs)
	}
}
