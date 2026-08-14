// Package providerutil holds two tiny helpers that every HTTP provider
// needs and that were previously copy-pasted per provider: extracting a
// human-readable message from an error response body, and merging
// ProviderOptions["<name>"] into an already-marshaled JSON request.
package providerutil

import (
	"encoding/json"
	"fmt"
	"maps"
)

// wireError is the superset of the error-body shapes the supported
// providers actually send: {"error":{"message":...}} (OpenAI, Anthropic),
// {"error":"..."} (BFL, Deepgram), {"message":"..."} (Cohere, Gladia,
// Hume, ...), {"err_msg":"..."} (Deepgram), {"detail"/"title"} (Rev.ai).
type wireError struct {
	Error   json.RawMessage `json:"error"`
	Message string          `json:"message"`
	ErrMsg  string          `json:"err_msg"`
	Detail  string          `json:"detail"`
	Title   string          `json:"title"`
}

// ErrorMessage extracts a message from an error response body, probing the
// known wire shapes most-specific first. Falls back to the raw body.
func ErrorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil {
		if len(we.Error) > 0 {
			var obj struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(we.Error, &obj); err == nil && obj.Message != "" {
				return obj.Message
			}
		}
		if we.Message != "" {
			return we.Message
		}
		if we.ErrMsg != "" {
			return we.ErrMsg
		}
		var errStr string
		if len(we.Error) > 0 && json.Unmarshal(we.Error, &errStr) == nil && errStr != "" {
			return errStr
		}
		if we.Detail != "" {
			return we.Detail
		}
		if we.Title != "" {
			return we.Title
		}
	}
	return string(body)
}

// ApplyProviderOptions merges providerOptions[name] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip)
// when there's nothing to merge, which is the common case.
func ApplyProviderOptions(reqBytes []byte, providerOptions map[string]any, name string) ([]byte, error) {
	opts, _ := providerOptions[name].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("%s: unmarshal request for provider options merge: %w", name, err)
	}
	maps.Copy(m, opts)
	return json.Marshal(m)
}
