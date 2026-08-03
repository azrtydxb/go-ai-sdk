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
