package voyage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type embeddingModel struct {
	provider *Provider
	modelID  string
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return providerName }
func (m *embeddingModel) MaxBatchSize() int    { return embeddingBatch }

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	return m.EmbedCall(ctx, provider.EmbeddingCall{Values: values})
}

func (m *embeddingModel) EmbedCall(ctx context.Context, call provider.EmbeddingCall) (*provider.EmbeddingResponse, error) {
	reqBody, err := json.Marshal(embeddingRequest{
		Model: m.modelID,
		Input: call.Values,
	})
	if err != nil {
		return nil, fmt.Errorf("voyage: marshal embedding request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("voyage: apply provider options: %w", err)
	}

	url := m.provider.baseURL + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("voyage: build embedding request: %w", err)
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
		return nil, fmt.Errorf("voyage: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr embeddingResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("voyage: decode embedding response: %w", err)
	}

	embeddings := make([][]float64, len(wr.Data))
	for _, d := range wr.Data {
		if d.Index < 0 || d.Index >= len(embeddings) {
			return nil, fmt.Errorf("voyage: embedding response index %d out of range [0,%d)", d.Index, len(embeddings))
		}
		embeddings[d.Index] = d.Embedding
	}

	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage: provider.Usage{
			TotalTokens: wr.Usage.TotalTokens,
		},
	}, nil
}
