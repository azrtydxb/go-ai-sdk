package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestListModelsMapsCall(t *testing.T) {
	m := &aitest.MockModelLister{Models: []provider.ModelInfo{{ID: "a"}, {ID: "b"}}}
	got, err := ListModels(t.Context(), ListModelsOpts{
		Lister:         m,
		OutputModality: "speech",
		InputModality:  "text",
		Headers:        map[string]string{"X-Test": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("models = %+v", got)
	}
	calls := m.RecordedCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if c := calls[0]; c.OutputModality != "speech" || c.InputModality != "text" || c.Headers["X-Test"] != "1" {
		t.Fatalf("call mapped incorrectly: %+v", c)
	}
}

func TestListModelsNilLister(t *testing.T) {
	if _, err := ListModels(t.Context(), ListModelsOpts{}); !errors.Is(err, ErrListerRequired) {
		t.Fatalf("err = %v, want ErrListerRequired", err)
	}
}

func TestListModelsRetriesRetryable(t *testing.T) {
	zero := 0
	m := &aitest.MockModelLister{Err: NewAPICallError(503, "u", "", "down")}
	if _, err := ListModels(t.Context(), ListModelsOpts{Lister: m, MaxRetries: &zero}); err == nil {
		t.Fatal("want error")
	}
	if n := len(m.RecordedCalls()); n != 1 {
		t.Fatalf("calls = %d, want 1 with MaxRetries 0", n)
	}
}

func TestCheckKey(t *testing.T) {
	limit := 10.0
	m := &aitest.MockKeyChecker{Info: &provider.KeyInfo{Detailed: true, Label: "sk-or-...abc", Limit: &limit}}
	info, err := CheckKey(t.Context(), CheckKeyOpts{Checker: m, Headers: map[string]string{"X-Test": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Detailed || info.Label != "sk-or-...abc" || *info.Limit != 10 {
		t.Fatalf("info = %+v", info)
	}
	if c := m.RecordedCalls(); len(c) != 1 || c[0].Headers["X-Test"] != "1" {
		t.Fatalf("calls = %+v", c)
	}
}

func TestCheckKeyRejectedNotRetried(t *testing.T) {
	m := &aitest.MockKeyChecker{Err: NewAPICallError(401, "u", "", "bad key")}
	_, err := CheckKey(t.Context(), CheckKeyOpts{Checker: m})
	var api *APICallError
	if !errors.As(err, &api) || api.StatusCode != 401 {
		t.Fatalf("err = %v, want *APICallError 401", err)
	}
	if n := len(m.RecordedCalls()); n != 1 {
		t.Fatalf("calls = %d, want 1 (401 is not retried)", n)
	}
}

func TestCheckKeyNilChecker(t *testing.T) {
	if _, err := CheckKey(t.Context(), CheckKeyOpts{}); !errors.Is(err, ErrCheckerRequired) {
		t.Fatalf("err = %v, want ErrCheckerRequired", err)
	}
}
