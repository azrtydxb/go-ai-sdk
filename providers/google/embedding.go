package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const embeddingMaxBatchSize = 100

type embeddingModel struct {
	provider *Provider
	modelID  string
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return "google" }
func (m *embeddingModel) MaxBatchSize() int    { return embeddingMaxBatchSize }

func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	reqs := make([]embedContentRequest, len(values))
	for i, v := range values {
		reqs[i] = embedContentRequest{
			Model:   "models/" + m.modelID,
			Content: wireContent{Parts: []wirePart{{Text: v}}},
		}
	}

	reqBody, err := json.Marshal(batchEmbedRequest{Requests: reqs})
	if err != nil {
		return nil, fmt.Errorf("google: marshal embedding request: %w", err)
	}

	url := m.provider.baseURL + "/models/" + m.modelID + ":batchEmbedContents"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("google: build embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("google: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr batchEmbedResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("google: decode embedding response: %w", err)
	}

	embeddings := make([][]float64, len(wr.Embeddings))
	for i, e := range wr.Embeddings {
		embeddings[i] = e.Values
	}

	// The batchEmbedContents API does not return usage information.
	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage:      provider.Usage{},
	}, nil
}
