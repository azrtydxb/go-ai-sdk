package cohere

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestCohereLogprobsUnsupported(t *testing.T) {
	top := 2
	for name, call := range map[string]provider.Call{
		"logprobs":     {Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true},
		"top_logprobs": {Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true, TopLogprobs: &top},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildChatRequest("command-r", call, false)
			var ufe *provider.UnsupportedFeatureError
			if !errors.As(err, &ufe) || ufe.Provider != "cohere" || ufe.Feature != "Logprobs" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCohereStrictFalseUnsupported(t *testing.T) {
	no := false
	_, err := buildChatRequest("command-r", provider.Call{
		Messages:       []provider.Message{provider.UserText("hi")},
		ResponseFormat: &provider.ResponseFormat{Type: "json", Schema: json.RawMessage(`{"type":"object"}`), Strict: &no},
	}, false)
	var ufe *provider.UnsupportedFeatureError
	if !errors.As(err, &ufe) || !strings.Contains(ufe.Feature, "Strict") {
		t.Fatalf("err = %v", err)
	}

	// Strict=true (or unset) and strict=false without a schema stay valid.
	yes := true
	for _, rf := range []*provider.ResponseFormat{
		{Type: "json", Schema: json.RawMessage(`{"type":"object"}`), Strict: &yes},
		{Type: "json", Strict: &no},
	} {
		if _, err := buildChatRequest("command-r", provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ResponseFormat: rf}, false); err != nil {
			t.Errorf("rf %+v: %v", rf, err)
		}
	}
}
