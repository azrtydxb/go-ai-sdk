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
			if !errors.Is(err, errTest) {
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

func collectLenient(t *testing.T, input string) []Event {
	t.Helper()
	var out []Event
	for ev, err := range ScanLenient(strings.NewReader(input)) {
		if err != nil {
			t.Fatalf("scan error: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

func TestScanLenientFlushesTrailingEvent(t *testing.T) {
	for name, in := range map[string]string{
		"no newline":      "data: a\n\ndata: tail",
		"single newline":  "data: a\n\ndata: tail\n",
		"crlf":            "data: a\r\n\r\ndata: tail\r\n",
		"comment between": "data: a\n\n: ping\ndata: tail\n: ping",
	} {
		t.Run(name, func(t *testing.T) {
			evs := collectLenient(t, in)
			if len(evs) != 2 || evs[0].Data != "a" || evs[1].Data != "tail" {
				t.Fatalf("evs = %#v", evs)
			}
		})
	}
}

func TestScanLenientFlushesNamedTrailingEvent(t *testing.T) {
	evs := collectLenient(t, "event: delta\ndata: a\ndata: b")
	if len(evs) != 1 || evs[0].Event != "delta" || evs[0].Data != "a\nb" {
		t.Fatalf("evs = %#v", evs)
	}
}

func TestScanLenientNothingPendingYieldsNothing(t *testing.T) {
	for _, in := range []string{"", "\n", "data: a\n\n", ": only a comment", ": c\r\n\r\n"} {
		evs := collectLenient(t, in)
		want := 0
		if in == "data: a\n\n" {
			want = 1
		}
		if len(evs) != want {
			t.Errorf("%q: evs = %#v", in, evs)
		}
	}
}

func TestScanLenientNoFlushAfterReadError(t *testing.T) {
	errTest := errors.New("boom")
	var events int
	var gotErr error
	for _, err := range ScanLenient(&errorReader{data: "data: partial", err: errTest}) {
		if err != nil {
			gotErr = err
			break
		}
		events++
	}
	if events != 0 || !errors.Is(gotErr, errTest) {
		t.Fatalf("events=%d err=%v", events, gotErr)
	}
}

func TestScanStrictCRLFAndComments(t *testing.T) {
	evs := collect(t, ": hi\r\nevent: e\r\ndata: a\r\n: mid\r\ndata: b\r\n\r\n: bye\r\n\r\ndata: tail\r\n")
	if len(evs) != 1 || evs[0].Event != "e" || evs[0].Data != "a\nb" {
		t.Fatalf("evs = %#v", evs)
	}
}
