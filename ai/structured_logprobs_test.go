package ai

import (
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func textResp(text string, lp []provider.TokenLogprob) *provider.Response {
	return &provider.Response{
		Content:      []provider.ContentPart{provider.TextPart{Text: text}},
		FinishReason: provider.FinishStop,
		Logprobs:     lp,
	}
}

func sampleLogprobs() []provider.TokenLogprob {
	return []provider.TokenLogprob{{
		Token: "yes", Logprob: -0.1,
		TopLogprobs: []provider.TopLogprob{{Token: "yes", Logprob: -0.1}, {Token: "no", Logprob: -2.5}},
	}}
}

func TestGenerateTextThreadsResponseFormatAndLogprobs(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{textResp("yes", sampleLogprobs())}}
	top := 2
	strict := false
	rf := &provider.ResponseFormat{Type: "json", Schema: []byte(`{"type":"object"}`), Name: "n", Strict: &strict}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "p", ResponseFormat: rf, Logprobs: true, TopLogprobs: &top,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := m.RecordedCalls()[0]
	if call.ResponseFormat != rf || !call.Logprobs || call.TopLogprobs == nil || *call.TopLogprobs != 2 {
		t.Errorf("call = %+v", call)
	}
	if len(res.Logprobs) != 1 || res.Logprobs[0].Token != "yes" || len(res.Logprobs[0].TopLogprobs) != 2 {
		t.Errorf("result logprobs = %+v", res.Logprobs)
	}
	if len(res.Steps[0].Logprobs) != 1 {
		t.Errorf("step logprobs = %+v", res.Steps[0].Logprobs)
	}
}

func TestStreamTextAccumulatesLogprobs(t *testing.T) {
	lp := sampleLogprobs()
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "yes"},
		provider.LogprobsDelta{Tokens: lp},
		provider.LogprobsDelta{Tokens: []provider.TokenLogprob{{Token: "!", Logprob: -1}}},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "p", Logprobs: true})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	got := s.Logprobs()
	if len(got) != 2 || got[0].Token != "yes" || got[1].Token != "!" {
		t.Fatalf("Logprobs() = %+v", got)
	}
	if res := s.buildResult(); len(res.Logprobs) != 2 {
		t.Errorf("buildResult().Logprobs = %+v", res.Logprobs)
	}
}

func TestOutputJSONSkipsResponseFormatWhenNoJSONObject(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caps   provider.Capabilities
		wantRF bool
	}{
		{"default", provider.Capabilities{}, true},
		{"no json object", provider.Capabilities{NoJSONObject: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &aitest.MockModel{Caps: tc.caps, Responses: []*provider.Response{textResp(`{"a":1}`, nil)}}
			if _, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "p", Output: OutputJSON()}); err != nil {
				t.Fatal(err)
			}
			if got := m.RecordedCalls()[0].ResponseFormat != nil; got != tc.wantRF {
				t.Errorf("ResponseFormat sent = %v, want %v", got, tc.wantRF)
			}
		})
	}
}

func TestSimulatedStreamingCarriesLogprobs(t *testing.T) {
	inner := &aitest.MockModel{Responses: []*provider.Response{textResp("yes", sampleLogprobs())}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: SimulateStreamingMiddleware(inner), Prompt: "p", Logprobs: true})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if len(s.Logprobs()) != 1 {
		t.Fatalf("Logprobs() = %+v", s.Logprobs())
	}
}
