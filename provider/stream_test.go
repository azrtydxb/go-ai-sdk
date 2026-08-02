package provider

import "testing"

func TestReasoningDeltaIsStreamPart(t *testing.T) {
	var _ StreamPart = ReasoningDelta{Text: "thinking..."}
}

func TestSourceEventIsStreamPart(t *testing.T) {
	var _ StreamPart = SourceEvent{Source: SourcePart{ID: "source_0", URL: "https://example.com"}}
}
