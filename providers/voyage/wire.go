package voyage

import (
	"encoding/json"
	"fmt"
)

// ---- Error wire type ----

type wireError struct {
	Detail string `json:"detail"`
}

// errorMessage tries to parse Voyage's {"detail":...} error body shape.
// Falls back to the raw body if parsing fails or no detail field is
// present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Detail != "" {
		return we.Detail
	}
	return string(body)
}

// applyProviderOptions merges providerOptions["voyage"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip) when
// there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["voyage"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("voyage: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

// ---- Embeddings wire types ----

type embeddingRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	InputType string   `json:"input_type,omitempty"`
}

type embeddingResponse struct {
	Data  []embeddingDataWire `json:"data"`
	Usage embeddingUsageWire  `json:"usage"`
}

type embeddingDataWire struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingUsageWire struct {
	TotalTokens int `json:"total_tokens"`
}

// ---- Rerank wire types ----

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      *int     `json:"top_k,omitempty"`
}

type rerankResponse struct {
	Data  []rerankResultWire `json:"data"`
	Usage rerankUsageWire    `json:"usage"`
}

type rerankResultWire struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankUsageWire struct {
	TotalTokens int `json:"total_tokens"`
}
