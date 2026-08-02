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
