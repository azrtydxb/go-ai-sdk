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

type embeddingModel struct {
	provider *Provider
	modelID  string
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return providerName }
func (m *embeddingModel) MaxBatchSize() int    { return embeddingBatch }

func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	reqBody, err := json.Marshal(embeddingRequest{
		Model:          m.modelID,
		Texts:          values,
		InputType:      "search_document",
		EmbeddingTypes: []string{"float"},
	})
	if err != nil {
		return nil, fmt.Errorf("cohere: marshal embedding request: %w", err)
	}

	url := m.provider.baseURL + "/embed"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("cohere: build embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cohere: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr embeddingResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("cohere: decode embedding response: %w", err)
	}

	inputTokens := int(wr.Meta.BilledUnits.InputTokens)
	return &provider.EmbeddingResponse{
		Embeddings: wr.Embeddings.Float,
		Usage: provider.Usage{
			InputTokens: inputTokens,
			TotalTokens: inputTokens,
		},
	}, nil
}
