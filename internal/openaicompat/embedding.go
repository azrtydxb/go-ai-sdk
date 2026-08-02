package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewEmbeddingModel returns a provider.EmbeddingModel that speaks the
// OpenAI embeddings wire format against cfg. Callers should only construct
// one when cfg.EmbedBatch > 0.
func NewEmbeddingModel(cfg Config, modelID string) provider.EmbeddingModel {
	return &embeddingModel{cfg: cfg, modelID: modelID}
}

type embeddingModel struct {
	cfg     Config
	modelID string
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return m.cfg.Name }
func (m *embeddingModel) MaxBatchSize() int    { return m.cfg.EmbedBatch }

func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}

	reqBody, err := json.Marshal(embeddingRequest{Model: m.modelID, Input: values})
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal embedding request: %w", err)
	}

	url := m.cfg.BaseURL + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	m.cfg.setAuthHeader(httpReq)

	resp, err := m.cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr embeddingResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode embedding response: %w", err)
	}

	embeddings := make([][]float64, len(wr.Data))
	for _, d := range wr.Data {
		if d.Index < 0 || d.Index >= len(embeddings) {
			return nil, fmt.Errorf("openaicompat: embedding index %d out of range [0,%d)", d.Index, len(embeddings))
		}
		embeddings[d.Index] = d.Embedding
	}
	for i, e := range embeddings {
		if e == nil {
			return nil, fmt.Errorf("openaicompat: embedding response missing index %d", i)
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
