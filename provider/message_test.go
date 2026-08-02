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
