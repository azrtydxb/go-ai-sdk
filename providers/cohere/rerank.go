package cohere

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

type rerankingModel struct {
	provider *Provider
	modelID  string
}

func (m *rerankingModel) ModelID() string      { return m.modelID }
func (m *rerankingModel) ProviderName() string { return providerName }

func (m *rerankingModel) Rerank(ctx context.Context, call provider.RerankCall) (*provider.RerankResponse, error) {
	req := rerankRequest{
		Model:     m.modelID,
		Query:     call.Query,
		Documents: call.Documents,
	}
	if call.TopN != 0 {
		topN := call.TopN
		req.TopN = &topN
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cohere: marshal rerank request: %w", err)
	}
	body, err = applyProviderOptions(body, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("cohere: apply provider options: %w", err)
	}

	url := m.provider.baseURL + "/rerank"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cohere: build rerank request: %w", err)
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
		return nil, fmt.Errorf("cohere: read rerank response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, respBody)
	}

	var wr rerankResponse
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return nil, fmt.Errorf("cohere: decode rerank response: %w", err)
	}

	results := make([]provider.RankedDocument, 0, len(wr.Results))
	for _, r := range wr.Results {
		results = append(results, provider.RankedDocument{
			Index: r.Index,
			Score: r.RelevanceScore,
		})
	}

	return &provider.RerankResponse{
		Results: results,
		// Cohere bills reranking in "search units", not tokens — Usage is
		// intentionally left zero here; see the raw body for billing info.
		Usage: provider.Usage{},
		Raw:   json.RawMessage(respBody),
	}, nil
}
