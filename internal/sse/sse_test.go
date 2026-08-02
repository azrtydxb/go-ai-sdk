package sse

import (
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
