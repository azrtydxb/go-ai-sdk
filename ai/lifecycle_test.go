package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---------------------------------------------------------------------
// GenerateText: OnModelCallStart/OnModelCallEnd
// ---------------------------------------------------------------------

// TestGenerateTextModelCallLifecycleFiresPerStep verifies Start/End fire
// once per step across a 2-step tool loop, with the exact stepIndex and a
// non-nil Response on End.
func TestGenerateTextModelCallLifecycleFiresPerStep(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "It is sunny."}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
	}}
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "sunny", nil
	})

	var startIdx, endIdx []int
	var endResponses []*provider.Response
	var endErrs []error

	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
		OnModelCallStart: func(stepIndex int, call provider.Call) {
			startIdx = append(startIdx, stepIndex)
		},
		OnModelCallEnd: func(end ModelCallEnd) {
			endIdx = append(endIdx, end.StepIndex)
			endResponses = append(endResponses, end.Response)
			endErrs = append(endErrs, end.Err)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if len(startIdx) != 2 || startIdx[0] != 0 || startIdx[1] != 1 {
		t.Fatalf("OnModelCallStart stepIndexes = %v, want [0 1]", startIdx)
	}
	if len(endIdx) != 2 || endIdx[0] != 0 || endIdx[1] != 1 {
		t.Fatalf("OnModelCallEnd stepIndexes = %v, want [0 1]", endIdx)
	}
	for i, r := range endResponses {
		if r == nil {
			t.Fatalf("endResponses[%d] = nil, want non-nil", i)
		}
	}
	for i, e := range endErrs {
		if e != nil {
			t.Fatalf("endErrs[%d] = %v, want nil", i, e)
		}
	}
}

// TestGenerateTextModelCallEndCarriesRetryError verifies OnModelCallStart
// fires exactly once (not per retry attempt) and OnModelCallEnd fires once,
// after the final attempt, with Err equal to the SAME *RetryError
// GenerateText itself returns.
func TestGenerateTextModelCallEndCarriesRetryError(t *testing.T) {
	m := &aitest.MockModel{Err: NewAPICallError(500, "https://x", "", "boom")}

	var startCalls, endCalls int
	var endErr error

	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "hi",
		OnModelCallStart: func(stepIndex int, call provider.Call) { startCalls++ },
		OnModelCallEnd: func(end ModelCallEnd) {
			endCalls++
			endErr = end.Err
		},
	})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v, want RetryError{Attempts:3}", err)
	}
	if startCalls != 1 {
		t.Fatalf("OnModelCallStart calls = %d, want 1 (not once per retry)", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnModelCallEnd calls = %d, want 1", endCalls)
	}
	if endErr != err {
		t.Fatalf("OnModelCallEnd Err = %v, want the exact error GenerateText returned (%v)", endErr, err)
	}
}

// ---------------------------------------------------------------------
// GenerateText: OnToolExecutionStart/OnToolExecutionEnd
// ---------------------------------------------------------------------

// multiToolCallResponse builds a Response containing several tool calls in
// one step.
func multiToolCallResponse(calls ...provider.ToolCallPart) *provider.Response {
	parts := make([]provider.ContentPart, len(calls))
	for i, c := range calls {
		parts[i] = c
	}
	return &provider.Response{
		Content:      parts,
		FinishReason: provider.FinishToolCalls,
		Usage:        provider.Usage{TotalTokens: 10},
	}
}

// TestGenerateTextToolExecutionLifecyclePerCall verifies Start/End fire
// once per tool call record (not per execution attempt) in a step with
// multiple tool calls, with the result record on End.
func TestGenerateTextToolExecutionLifecyclePerCall(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		multiToolCallResponse(
			provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)},
			provider.ToolCallPart{ID: "c2", Name: "get_weather", Args: []byte(`{"city":"Bruges"}`)},
		),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "sunny-" + a.City, nil
	})

	var startCalls []ToolCallRecord
	var startSteps []int
	var endResults []ToolResultRecord
	var endSteps []int
	var endErrs []error

	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
		OnToolExecutionStart: func(stepIndex int, call ToolCallRecord) {
			startSteps = append(startSteps, stepIndex)
			startCalls = append(startCalls, call)
		},
		OnToolExecutionEnd: func(stepIndex int, result ToolResultRecord, err error) {
			endSteps = append(endSteps, stepIndex)
			endResults = append(endResults, result)
			endErrs = append(endErrs, err)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(startCalls) != 2 || len(endResults) != 2 {
		t.Fatalf("Start calls = %d, End calls = %d, want 2 each", len(startCalls), len(endResults))
	}
	for i, s := range startSteps {
		if s != 0 {
			t.Fatalf("startSteps[%d] = %d, want 0", i, s)
		}
	}
	for i, s := range endSteps {
		if s != 0 {
			t.Fatalf("endSteps[%d] = %d, want 0", i, s)
		}
	}
	if startCalls[0].ID != "c1" || startCalls[1].ID != "c2" {
		t.Fatalf("startCalls IDs = [%q %q], want [c1 c2]", startCalls[0].ID, startCalls[1].ID)
	}
	if endResults[0].ToolCallID != "c1" || endResults[0].Result != "sunny-Ghent" {
		t.Fatalf("endResults[0] = %+v", endResults[0])
	}
	if endResults[1].ToolCallID != "c2" || endResults[1].Result != "sunny-Bruges" {
		t.Fatalf("endResults[1] = %+v", endResults[1])
	}
	for i, e := range endErrs {
		if e != nil {
			t.Fatalf("endErrs[%d] = %v, want nil", i, e)
		}
	}
}

// TestGenerateTextToolExecutionLifecycleOnePairPerRepairRetry verifies that
// when RepairToolCall retries a failed Execute, the whole
// execute-and-maybe-repair sequence counts as ONE Start/End pair, not two.
func TestGenerateTextToolExecutionLifecycleOnePairPerRepairRetry(t *testing.T) {
	attempts := 0
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		attempts++
		if a.City == "nowhere" {
			return nil, &InvalidToolArgumentsError{ToolName: "get_weather", Cause: errors.New("bad args")}
		}
		return "sunny", nil
	})
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"nowhere"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}

	var startCalls, endCalls int
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
		RepairToolCall: func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			return ToolCallRecord{ID: call.ID, Name: call.Name, Args: []byte(`{"city":"Ghent"}`)}, true
		},
		OnToolExecutionStart: func(stepIndex int, call ToolCallRecord) { startCalls++ },
		OnToolExecutionEnd:   func(stepIndex int, result ToolResultRecord, err error) { endCalls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("tool Execute attempts = %d, want 2 (original + repaired)", attempts)
	}
	if startCalls != 1 {
		t.Fatalf("OnToolExecutionStart calls = %d, want 1 (one pair per call record, not per attempt)", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnToolExecutionEnd calls = %d, want 1", endCalls)
	}
}

// TestGenerateTextToolExecutionLifecycleRepairChangesID pins the documented
// behavior of OnToolExecutionStart/OnToolExecutionEnd when RepairToolCall's
// bad-args repair path changes the call's ID (and Name): Start fires with
// the ORIGINAL (pre-repair) ID/Name — it fires before Execute is first
// attempted, before repair has run — while End's ToolResultRecord carries
// the REPAIRED ID/Name that was actually executed and recorded. The pair
// must therefore be correlated by call order, not by ID, whenever repair
// may rename a call.
func TestGenerateTextToolExecutionLifecycleRepairChangesID(t *testing.T) {
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		if a.City == "nowhere" {
			return nil, &InvalidToolArgumentsError{ToolName: "get_weather", Cause: errors.New("bad args")}
		}
		return "sunny", nil
	})
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "orig-id", `{"city":"nowhere"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}

	var startCall ToolCallRecord
	var endResult ToolResultRecord
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
		RepairToolCall: func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			// Repair changes both ID and Name (as well as fixing Args).
			return ToolCallRecord{ID: "repaired-id", Name: call.Name, Args: []byte(`{"city":"Ghent"}`)}, true
		},
		OnToolExecutionStart: func(stepIndex int, call ToolCallRecord) { startCall = call },
		OnToolExecutionEnd:   func(stepIndex int, result ToolResultRecord, err error) { endResult = result },
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCall.ID != "orig-id" {
		t.Fatalf("OnToolExecutionStart call.ID = %q, want %q (original, pre-repair)", startCall.ID, "orig-id")
	}
	if endResult.ToolCallID != "repaired-id" {
		t.Fatalf("OnToolExecutionEnd result.ToolCallID = %q, want %q (repaired)", endResult.ToolCallID, "repaired-id")
	}
	if endResult.Result != "sunny" {
		t.Fatalf("OnToolExecutionEnd result.Result = %v, want %q", endResult.Result, "sunny")
	}
	if endResult.Err != nil {
		t.Fatalf("OnToolExecutionEnd err = %v, want nil", endResult.Err)
	}
}

// TestGenerateTextOutputToolModeFallbackSkipsToolExecutionCallbacks verifies
// that the Output tool-mode fallback's forced tool call (never executed via
// runToolCalls) does NOT fire OnToolExecutionStart/End, while
// OnModelCallStart/End fire normally for its single model call.
func TestGenerateTextOutputToolModeFallbackSkipsToolExecutionCallbacks(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: defaultSchemaName, Args: []byte(`{"title":"Dune","pages":412}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}

	var toolStartCalls, toolEndCalls, modelStartCalls, modelEndCalls int
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "book", Output: OutputObject[outBook](),
		OnToolExecutionStart: func(stepIndex int, call ToolCallRecord) { toolStartCalls++ },
		OnToolExecutionEnd:   func(stepIndex int, result ToolResultRecord, err error) { toolEndCalls++ },
		OnModelCallStart:     func(stepIndex int, call provider.Call) { modelStartCalls++ },
		OnModelCallEnd:       func(end ModelCallEnd) { modelEndCalls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(res.Steps))
	}
	if toolStartCalls != 0 || toolEndCalls != 0 {
		t.Fatalf("tool execution callbacks fired for the synthetic forced call: start=%d end=%d, want 0 each", toolStartCalls, toolEndCalls)
	}
	if modelStartCalls != 1 || modelEndCalls != 1 {
		t.Fatalf("model call callbacks = start:%d end:%d, want 1 each", modelStartCalls, modelEndCalls)
	}
}

// ---------------------------------------------------------------------
// StreamText: OnModelCallStart/OnModelCallEnd
// ---------------------------------------------------------------------

// TestStreamTextModelCallStartFiresBeforePartsFlow verifies OnModelCallStart
// fires before any parts are yielded to the consumer.
func TestStreamTextModelCallStartFiresBeforePartsFlow(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}

	var events []string
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x",
		OnModelCallStart: func(stepIndex int, call provider.Call) { events = append(events, "start") },
	})
	if err != nil {
		t.Fatal(err)
	}
	// OnModelCallStart for step 0 fires inside StreamText itself, before
	// Parts() is ever ranged over.
	if len(events) != 1 || events[0] != "start" {
		t.Fatalf("events before Parts() = %v, want [start]", events)
	}
	for range s.Parts() {
		events = append(events, "part")
	}
	if events[0] != "start" {
		t.Fatalf("events = %v, want start before any part", events)
	}
}

// TestStreamTextModelCallEndCarriesFinishPartUsage verifies OnModelCallEnd
// fires once per step with Usage/FinishReason from that step's FinishPart,
// Response nil (StreamText has no single Response), and Err nil.
func TestStreamTextModelCallEndCarriesFinishPartUsage(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 7}},
	}}}

	var ends []ModelCallEnd
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x",
		OnModelCallEnd: func(end ModelCallEnd) { ends = append(ends, end) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if len(ends) != 1 {
		t.Fatalf("OnModelCallEnd calls = %d, want 1", len(ends))
	}
	end := ends[0]
	if end.StepIndex != 0 {
		t.Fatalf("StepIndex = %d, want 0", end.StepIndex)
	}
	if end.Response != nil {
		t.Fatalf("Response = %v, want nil (StreamText)", end.Response)
	}
	if end.Usage.TotalTokens != 7 {
		t.Fatalf("Usage = %+v, want TotalTokens=7", end.Usage)
	}
	if end.FinishReason != provider.FinishStop {
		t.Fatalf("FinishReason = %v, want FinishStop", end.FinishReason)
	}
	if end.Err != nil {
		t.Fatalf("Err = %v, want nil", end.Err)
	}
}

// TestStreamTextModelCallEndFiresWithErrOnOrdinaryStreamError verifies that
// a genuine (non-ctx-cancel) mid-stream error fires OnModelCallEnd with Err
// set and zero Usage.
func TestStreamTextModelCallEndFiresWithErrOnOrdinaryStreamError(t *testing.T) {
	wantErr := errors.New("boom mid-stream")
	inner := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	errModel := &errStreamModel{inner: inner, err: wantErr}

	var ends []ModelCallEnd
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: errModel, Prompt: "x",
		OnModelCallEnd: func(end ModelCallEnd) { ends = append(ends, end) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if len(ends) != 1 {
		t.Fatalf("OnModelCallEnd calls = %d, want 1", len(ends))
	}
	if !errors.Is(ends[0].Err, wantErr) {
		t.Fatalf("Err = %v, want %v", ends[0].Err, wantErr)
	}
	if ends[0].Usage != (provider.Usage{}) {
		t.Fatalf("Usage = %+v, want zero value", ends[0].Usage)
	}
}

// TestStreamTextModelCallEndNotFiredOnAbandon verifies that OnModelCallEnd
// does NOT fire when the consumer abandons Parts() early (OnAbort covers
// that termination instead).
func TestStreamTextModelCallEndNotFiredOnAbandon(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"}, provider.TextDelta{Text: "b"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	var aborted, modelEnds int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x",
		OnAbort:        func() { aborted++ },
		OnModelCallEnd: func(end ModelCallEnd) { modelEnds++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
		break
	}
	if aborted != 1 {
		t.Fatalf("OnAbort calls = %d, want 1", aborted)
	}
	if modelEnds != 0 {
		t.Fatalf("OnModelCallEnd calls = %d, want 0 (abandon fires OnAbort, not OnModelCallEnd)", modelEnds)
	}
}

// TestStreamTextModelCallEndNotFiredOnCtxCancelMidStream verifies that when
// ctx cancellation is what caused a mid-stream termination, OnModelCallEnd
// does NOT fire for that step (OnAbort covers it instead).
func TestStreamTextModelCallEndNotFiredOnCtxCancelMidStream(t *testing.T) {
	inner := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	errModel := &errStreamModel{inner: inner, err: context.Canceled}

	var aborted, modelEnds int
	s, err := StreamText(ctx, GenerateTextOpts{
		Model: errModel, Prompt: "x",
		OnAbort:        func() { aborted++ },
		OnModelCallEnd: func(end ModelCallEnd) { modelEnds++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for range s.Parts() {
	}
	if aborted != 1 {
		t.Fatalf("OnAbort calls = %d, want 1", aborted)
	}
	if modelEnds != 0 {
		t.Fatalf("OnModelCallEnd calls = %d, want 0 (ctx-cancel fires OnAbort only)", modelEnds)
	}
}

// TestStreamTextLifecycleCallbacksRace exercises all four lifecycle
// callbacks (model-call and tool-execution) across a 2-step tool loop,
// appending to a shared (unsynchronized) slice from within the callbacks.
// Since callbacks fire synchronously on the goroutine driving Parts(), this
// must be race-free under `go test -race`.
func TestStreamTextLifecycleCallbacksRace(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "sunny", nil
	})

	var events []string
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
		OnModelCallStart:     func(stepIndex int, call provider.Call) { events = append(events, "model-start") },
		OnModelCallEnd:       func(end ModelCallEnd) { events = append(events, "model-end") },
		OnToolExecutionStart: func(stepIndex int, call ToolCallRecord) { events = append(events, "tool-start") },
		OnToolExecutionEnd:   func(stepIndex int, result ToolResultRecord, err error) { events = append(events, "tool-end") },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	want := []string{"model-start", "model-end", "tool-start", "tool-end", "model-start", "model-end"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// ---------------------------------------------------------------------
// Embed / EmbedMany: OnEmbedStart/OnEmbedEnd
// ---------------------------------------------------------------------

func TestEmbedLifecycleCallbacksFireOnSuccess(t *testing.T) {
	m := &aitest.MockEmbedder{}
	var startVals []string
	var endResp *provider.EmbeddingResponse
	var endErr error
	var startCalls, endCalls int

	_, err := Embed(t.Context(), EmbedOpts{
		Model: m, Value: "hello",
		OnEmbedStart: func(values []string) { startCalls++; startVals = values },
		OnEmbedEnd: func(resp *provider.EmbeddingResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 || endCalls != 1 {
		t.Fatalf("startCalls=%d endCalls=%d, want 1 each", startCalls, endCalls)
	}
	if len(startVals) != 1 || startVals[0] != "hello" {
		t.Fatalf("startVals = %v, want [hello]", startVals)
	}
	if endResp == nil || endErr != nil {
		t.Fatalf("OnEmbedEnd args = (%v, %v), want (non-nil, nil)", endResp, endErr)
	}
}

func TestEmbedLifecycleCallbacksFireOnFinalError(t *testing.T) {
	m := &aitest.MockEmbedder{Err: NewAPICallError(500, "https://x", "", "boom")}
	var endCalls int
	var endErr error

	_, err := Embed(t.Context(), EmbedOpts{
		Model: m, Value: "hello", MaxRetries: intPtr(0),
		OnEmbedEnd: func(resp *provider.EmbeddingResponse, err error) {
			endCalls++
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if endCalls != 1 {
		t.Fatalf("endCalls = %d, want 1", endCalls)
	}
	if endErr != err {
		t.Fatalf("OnEmbedEnd Err = %v, want the exact error Embed returned (%v)", endErr, err)
	}
}

func TestEmbedManyLifecycleCallbacksPerBatchInOrder(t *testing.T) {
	m := &aitest.MockEmbedder{BatchSize: 2}
	var startBatches [][]string
	var endCalls int

	_, err := EmbedMany(t.Context(), EmbedManyOpts{
		Model:  m,
		Values: []string{"a", "b", "c", "d"},
		OnEmbedStart: func(values []string) {
			startBatches = append(startBatches, append([]string(nil), values...))
		},
		OnEmbedEnd: func(resp *provider.EmbeddingResponse, err error) { endCalls++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(startBatches) != 2 {
		t.Fatalf("startBatches = %v, want 2 batches", startBatches)
	}
	if endCalls != 2 {
		t.Fatalf("endCalls = %d, want 2", endCalls)
	}
	if startBatches[0][0] != "a" || startBatches[0][1] != "b" {
		t.Fatalf("startBatches[0] = %v, want [a b]", startBatches[0])
	}
	if startBatches[1][0] != "c" || startBatches[1][1] != "d" {
		t.Fatalf("startBatches[1] = %v, want [c d]", startBatches[1])
	}
}

// batchFailEmbedder is a provider.EmbeddingModel test double whose Embed
// call fails (with a non-retryable error, so retry.Do returns it
// immediately without wrapping) whenever the values slice's first element
// equals failValue; succeeds otherwise.
type batchFailEmbedder struct {
	failValue string
	calls     [][]string
}

func (e *batchFailEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	e.calls = append(e.calls, values)
	if len(values) > 0 && values[0] == e.failValue {
		return nil, NewAPICallError(400, "https://x", "", "bad batch")
	}
	embeddings := make([][]float64, len(values))
	for i := range embeddings {
		embeddings[i] = []float64{1, 2, 3}
	}
	return &provider.EmbeddingResponse{Embeddings: embeddings, Usage: provider.Usage{TotalTokens: len(values)}}, nil
}
func (e *batchFailEmbedder) MaxBatchSize() int    { return 2 }
func (e *batchFailEmbedder) ModelID() string      { return "mock-batch-fail" }
func (e *batchFailEmbedder) ProviderName() string { return "aitest" }

func TestEmbedManyLifecycleCallbacksEndWithErrOnFailingBatch(t *testing.T) {
	m := &batchFailEmbedder{failValue: "c"}
	var endResps []*provider.EmbeddingResponse
	var endErrs []error

	_, err := EmbedMany(t.Context(), EmbedManyOpts{
		Model:  m,
		Values: []string{"a", "b", "c", "d"},
		OnEmbedEnd: func(resp *provider.EmbeddingResponse, err error) {
			endResps = append(endResps, resp)
			endErrs = append(endErrs, err)
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if len(endResps) != 2 {
		t.Fatalf("OnEmbedEnd calls = %d, want 2 (first batch ok, second fails and stops the loop)", len(endResps))
	}
	if endErrs[0] != nil {
		t.Fatalf("endErrs[0] = %v, want nil", endErrs[0])
	}
	if endResps[0] == nil {
		t.Fatal("endResps[0] = nil, want non-nil")
	}
	if endErrs[1] == nil {
		t.Fatal("endErrs[1] = nil, want the batch's error")
	}
	if endResps[1] != nil {
		t.Fatalf("endResps[1] = %v, want nil (batch failed)", endResps[1])
	}
	if endErrs[1] != err {
		t.Fatalf("endErrs[1] = %v, want the exact error EmbedMany returned (%v)", endErrs[1], err)
	}
}

// ---------------------------------------------------------------------
// Rerank: OnRerankEnd sees the translated *RetryError, not the raw
// *retry.ExhaustedError (this is the convention Task 4 owns; see rerank.go).
// ---------------------------------------------------------------------

func TestRerankOnRerankEndReceivesTranslatedRetryError(t *testing.T) {
	m := &mockReranker{
		Err:      NewAPICallError(500, "https://x", "", "boom"),
		ErrCount: 100,
	}
	var endErr error
	_, err := Rerank(t.Context(), RerankOpts{
		Model: m, Query: "q", Documents: []string{"a"},
		OnRerankEnd: func(resp *provider.RerankResponse, err error) { endErr = err },
	})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v, want RetryError{Attempts:3}", err)
	}
	if endErr != err {
		t.Fatalf("OnRerankEnd Err = %v, want the exact *RetryError Rerank returned (%v)", endErr, err)
	}
	if _, ok := endErr.(*RetryError); !ok {
		t.Fatalf("OnRerankEnd Err type = %T, want *RetryError", endErr)
	}
}
