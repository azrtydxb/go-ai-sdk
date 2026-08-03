package mixedbread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// rerankRequest is Mixedbread's rerank request shape. Note the field names
// differ from Cohere/Voyage: "input" (not "documents") and "return_input"
// controls whether Mixedbread echoes the input documents back in the
// response (always false here — this SDK reconstructs order/documents from
// RerankCall.Documents and RankedDocument.Index).
type rerankRequest struct {
	Model       string   `json:"model"`
	Query       string   `json:"query"`
	Input       []string `json:"input"`
	TopK        *int     `json:"top_k,omitempty"`
	ReturnInput bool     `json:"return_input"`
}

type rerankResponse struct {
	Data []rerankResultWire `json:"data"`
}

// rerankResultWire is Mixedbread's per-result shape: "score", not
// "relevance_score" (the field name Cohere and Voyage use).
type rerankResultWire struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type rerankingModel struct {
	provider *Provider
	modelID  string
}

func (m *rerankingModel) ModelID() string      { return m.modelID }
func (m *rerankingModel) ProviderName() string { return providerName }

func (m *rerankingModel) Rerank(ctx context.Context, call provider.RerankCall) (*provider.RerankResponse, error) {
	req := rerankRequest{
		Model:       m.modelID,
		Query:       call.Query,
		Input:       call.Documents,
		ReturnInput: false,
	}
	if call.TopN != 0 {
		topK := call.TopN
		req.TopK = &topK
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mixedbread: marshal rerank request: %w", err)
	}
	body, err = applyProviderOptions(body, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("mixedbread: apply provider options: %w", err)
	}

	url := m.provider.baseURL + "/rerank"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mixedbread: build rerank request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mixedbread: read rerank response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, respBody)
	}

	var wr rerankResponse
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return nil, fmt.Errorf("mixedbread: decode rerank response: %w", err)
	}

	results := make([]provider.RankedDocument, 0, len(wr.Data))
	for _, r := range wr.Data {
		results = append(results, provider.RankedDocument{
			Index: r.Index,
			Score: r.Score,
		})
	}

	return &provider.RerankResponse{
		Results: results,
		// Usage is left zero-valued: Mixedbread's documented /rerank
		// response shape (`{"data":[{"index","score"}]}`) carries no
		// usage/total_tokens block, unlike Voyage's rerank response — there
		// is nothing to map here (same precedent as Cohere, which bills
		// rerank outside of a token-usage field).
		Usage: provider.Usage{},
		Raw:   json.RawMessage(respBody),
	}, nil
}
