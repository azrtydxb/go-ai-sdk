package providerutil

import "testing"

func TestApplyProviderOptionsKeepsBaseNestedBytes(t *testing.T) {
	base := []byte(`{"a":{"z":1,"b":12345678901234567890},"temperature":0.1}`)
	out, err := ApplyProviderOptions(base, map[string]any{"p": map[string]any{"temperature": 0.9}}, "p")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"z":1,"b":12345678901234567890},"temperature":0.9}`
	if string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}
