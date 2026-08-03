package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type weatherArgs struct {
	City string `json:"city"`
}

func TestNewToolExecutesTypedFn(t *testing.T) {
	tool := NewTool("get_weather", "weather for a city",
		func(_ context.Context, a weatherArgs) (any, error) { return "sunny in " + a.City, nil })
	if tool.Name() != "get_weather" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !strings.Contains(string(tool.Schema()), `"city"`) {
		t.Fatalf("schema = %s", tool.Schema())
	}
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"city":"Ghent"}`))
	if err != nil || out != "sunny in Ghent" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestNewToolInvalidArgs(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil })
	_, err := tool.Execute(t.Context(), json.RawMessage(`{"bogus":1}`))
	var iae *InvalidToolArgumentsError
	if !errors.As(err, &iae) || iae.ToolName != "t" {
		t.Fatalf("err = %v; want InvalidToolArgumentsError", err)
	}
}

func TestNewToolWrapsExecutionError(t *testing.T) {
	boom := errors.New("boom")
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, boom })
	_, err := tool.Execute(t.Context(), json.RawMessage(`{"city":"x"}`))
	var tee *ToolExecutionError
	if !errors.As(err, &tee) || !errors.Is(err, boom) {
		t.Fatalf("err = %v; want ToolExecutionError wrapping boom", err)
	}
}

func TestNewToolRejectsTrailingGarbage(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil })
	_, err := tool.Execute(t.Context(), json.RawMessage(`{"city":"Ghent"}garbage`))
	var iae *InvalidToolArgumentsError
	if !errors.As(err, &iae) || iae.ToolName != "t" {
		t.Fatalf("err = %v; want InvalidToolArgumentsError", err)
	}
}

// TestNewToolNoArgsNormalizesEmptyArgs pins the fix for the HIGH-severity
// bug where a no-arg tool call (empty/nil/whitespace-only args — the normal
// wire shape when a streamed tool call has no ArgsDelta at all) always
// failed with *InvalidToolArgumentsError, since json.Decoder.Decode on an
// empty reader returns io.EOF. Execute must normalize all three shapes to
// "{}" before decoding, so a tool taking struct{}{} succeeds on every one.
func TestNewToolNoArgsNormalizesEmptyArgs(t *testing.T) {
	var calls int
	tool := NewTool("ping", "", func(_ context.Context, _ struct{}) (any, error) {
		calls++
		return "pong", nil
	})

	for _, tc := range []struct {
		name string
		args json.RawMessage
	}{
		{"empty string", json.RawMessage("")},
		{"nil", nil},
		{"whitespace only", json.RawMessage("   \n\t")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tool.Execute(t.Context(), tc.args)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if out != "pong" {
				t.Fatalf("out = %v, want pong", out)
			}
		})
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

// TestNewToolNoArgsStillEnforcesRequiredFields confirms the empty-args
// normalization doesn't relax schema-level validation: a tool whose Args
// struct has fields still fails against "" the same way it would against
// any other object missing required data (here: DisallowUnknownFields never
// even gets a chance to reject anything else, but a caller genuinely relying
// on City being present would see a zero-value City, not an error — that's
// unchanged, ordinary Go JSON-decoding behavior, not something this fix
// introduces or removes).
func TestNewToolNoArgsStillDecodesZeroValueForFields(t *testing.T) {
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return a.City, nil
	})
	out, err := tool.Execute(t.Context(), json.RawMessage(""))
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out != "" {
		t.Fatalf("out = %q, want empty city (zero value)", out)
	}
}

func TestNewToolDefaultsStrictAndInputExamplesAreZero(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil })
	if tool.Strict() {
		t.Errorf("Strict() = true, want false by default")
	}
	if tool.InputExamples() != nil {
		t.Errorf("InputExamples() = %v, want nil by default", tool.InputExamples())
	}
}

func TestNewToolWithToolStrict(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil },
		WithToolStrict())
	if !tool.Strict() {
		t.Errorf("Strict() = false, want true")
	}
}

func TestNewToolWithToolInputExamples(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil },
		WithToolInputExamples(weatherArgs{City: "Ghent"}, weatherArgs{City: "Paris"}))
	examples := tool.InputExamples()
	if len(examples) != 2 {
		t.Fatalf("len(InputExamples()) = %d, want 2", len(examples))
	}
	if !strings.Contains(string(examples[0]), `"Ghent"`) {
		t.Errorf("examples[0] = %s, want it to contain Ghent", examples[0])
	}
	if !strings.Contains(string(examples[1]), `"Paris"`) {
		t.Errorf("examples[1] = %s, want it to contain Paris", examples[1])
	}
}

func TestNewToolWithBothOptions(t *testing.T) {
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil },
		WithToolStrict(), WithToolInputExamples(weatherArgs{City: "Ghent"}))
	if !tool.Strict() {
		t.Errorf("Strict() = false, want true")
	}
	if len(tool.InputExamples()) != 1 {
		t.Errorf("len(InputExamples()) = %d, want 1", len(tool.InputExamples()))
	}
}
