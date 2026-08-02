package vertex

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

// Vertex AI's text-embedding models are served through the generic Vertex
// Prediction API (:predict), which speaks a different wire format from
// Gemini's generateContent/batchEmbedContents — so unlike the language
// model, this does not go through internal/geminicompat.

type wireInstance struct {
	Content string `json:"content"`
}

type predictRequest struct {
	Instances []wireInstance `json:"instances"`
}

type wireEmbeddingStatistics struct {
	TokenCount int `json:"token_count"`
}

type wireEmbeddings struct {
	Values     []float64               `json:"values"`
	Statistics wireEmbeddingStatistics `json:"statistics"`
}

type wirePrediction struct {
	Embeddings wireEmbeddings `json:"embeddings"`
}

type predictResponse struct {
	Predictions []wirePrediction `json:"predictions"`
}

type wireError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Error.Message != "" {
		return we.Error.Message
	}
	return string(body)
}

type embeddingModel struct {
	p       *Provider
	modelID string
}

func newEmbeddingModel(p *Provider, modelID string) provider.EmbeddingModel {
	return &embeddingModel{p: p, modelID: modelID}
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return "vertex" }
func (m *embeddingModel) MaxBatchSize() int    { return embeddingMaxBatchSize }

// Embed calls Vertex AI's :predict endpoint with one instance per input
// value, returning the embedding values for each and the summed
// token-count statistics as Usage.
func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	instances := make([]wireInstance, len(values))
	for i, v := range values {
		instances[i] = wireInstance{Content: v}
	}

	reqBody, err := json.Marshal(predictRequest{Instances: instances})
	if err != nil {
		return nil, fmt.Errorf("vertex: marshal embedding request: %w", err)
	}

	url := m.p.endpointFor(m.modelID, "predict")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("vertex: build embedding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := m.p.authorize(ctx, httpReq); err != nil {
		return nil, fmt.Errorf("vertex: authorize request: %w", err)
	}

	client := m.p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vertex: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
	}

	var pr predictResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("vertex: decode embedding response: %w", err)
	}

	embeddings := make([][]float64, len(pr.Predictions))
	totalTokens := 0
	for i, p := range pr.Predictions {
		embeddings[i] = p.Embeddings.Values
		totalTokens += p.Embeddings.Statistics.TokenCount
	}

	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage:      provider.Usage{InputTokens: totalTokens, TotalTokens: totalTokens},
	}, nil
}
