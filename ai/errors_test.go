package ai

import (
	"errors"
	"testing"
)

func TestAPICallErrorRetryable(t *testing.T) {
	for _, tc := range []struct {
		code int
		want bool
	}{
		{429, true}, {500, true}, {503, true}, {408, true},
		{400, false}, {401, false}, {404, false},
	} {
		e := NewAPICallError(tc.code, "https://x", "", "boom")
		if e.Retryable != tc.want {
			t.Errorf("code %d: Retryable = %v, want %v", tc.code, e.Retryable, tc.want)
		}
	}
}

func TestErrorsUnwrap(t *testing.T) {
	cause := errors.New("cause")
	var err error = &ToolExecutionError{ToolName: "t", Cause: cause}
	if !errors.Is(err, cause) {
		t.Fatal("ToolExecutionError should unwrap to cause")
	}
}
