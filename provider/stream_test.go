package provider

import "testing"

func TestReasoningDeltaIsStreamPart(t *testing.T) {
	var _ StreamPart = ReasoningDelta{Text: "thinking..."}
}
