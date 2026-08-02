package mistral

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
	reqBody, err := json.Marshal(embeddingRequest{Model: m.modelID, Input: values})
	if err != nil {
		return nil, fmt.Errorf("mistral: marshal embedding request: %w", err)
	}

	url := m.provider.baseURL + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("mistral: build embedding request: %w", err)
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
		return nil, fmt.Errorf("mistral: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr embeddingResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("mistral: decode embedding response: %w", err)
	}

	embeddings := make([][]float64, len(wr.Data))
	for _, d := range wr.Data {
		if d.Index < 0 || d.Index >= len(embeddings) {
			return nil, fmt.Errorf("mistral: embedding index %d out of range [0,%d)", d.Index, len(embeddings))
		}
		embeddings[d.Index] = d.Embedding
	}
	for i, e := range embeddings {
		if e == nil {
			return nil, fmt.Errorf("mistral: embedding response missing index %d", i)
		}
	}

	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage: provider.Usage{
			InputTokens: wr.Usage.PromptTokens,
			TotalTokens: wr.Usage.TotalTokens,
		},
	}, nil
}
