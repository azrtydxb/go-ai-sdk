package provider

import "testing"

func TestHelpersBuildMessages(t *testing.T) {
	m := UserText("hi")
	if m.Role != RoleUser {
		t.Fatalf("role = %q, want %q", m.Role, RoleUser)
	}
	tp, ok := m.Content[0].(TextPart)
	if !ok || tp.Text != "hi" {
		t.Fatalf("content = %#v, want TextPart{hi}", m.Content[0])
	}
}

func TestResponseTextConcatenates(t *testing.T) {
	r := &Response{Content: []ContentPart{TextPart{"a"}, ToolCallPart{ID: "1"}, TextPart{"b"}}}
	if got := r.Text(); got != "ab" {
		t.Fatalf("Text() = %q, want %q", got, "ab")
	}
	if calls := r.ToolCalls(); len(calls) != 1 || calls[0].ID != "1" {
		t.Fatalf("ToolCalls() = %#v", calls)
	}
}

func TestResponseReasoningTextConcatenates(t *testing.T) {
	r := &Response{Content: []ContentPart{
		ReasoningPart{Text: "let me think "},
		TextPart{"answer"},
		ReasoningPart{Text: "...done", Signature: "sig"},
	}}
	if got := r.ReasoningText(); got != "let me think ...done" {
		t.Fatalf("ReasoningText() = %q, want %q", got, "let me think ...done")
	}
	if got := r.Text(); got != "answer" {
		t.Fatalf("Text() = %q, want %q (reasoning must not leak)", got, "answer")
	}
}

func TestReasoningPartIsContentPart(t *testing.T) {
	var _ ContentPart = ReasoningPart{Text: "x", Redacted: true, Signature: "sig"}
}

func TestUsageDetailFields(t *testing.T) {
	u := Usage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 20, ReasoningTokens: 10}
	if u.CachedInputTokens != 20 || u.ReasoningTokens != 10 {
		t.Fatalf("usage details not set: %#v", u)
	}
}
