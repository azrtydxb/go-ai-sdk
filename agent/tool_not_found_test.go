package agent

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestToolNotFoundPolicyPassthrough(t *testing.T) {
	newModel := func() *aitest.MockModel {
		return &aitest.MockModel{Responses: []*provider.Response{toolCallResponse("bogus", "c1", `{}`), textResponse("done")}}
	}
	a := &Agent{Model: newModel(), Tools: []ai.Tool{echoTool("echo")}}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "x"})
	if err != nil || res.Text != "done" {
		t.Fatalf("report: res=%v err=%v", res, err)
	}
	a = &Agent{Model: newModel(), Tools: []ai.Tool{echoTool("echo")}, ToolNotFound: ai.ToolNotFoundFail}
	_, err = a.Generate(t.Context(), RunOpts{Prompt: "x"})
	var nst *ai.NoSuchToolError
	if !errors.As(err, &nst) {
		t.Fatalf("fail: err = %v", err)
	}
}
