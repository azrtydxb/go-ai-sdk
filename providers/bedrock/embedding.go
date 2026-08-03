package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// Amazon Titan Text Embeddings only accepts one input text per invocation
// (unlike Bedrock's other embedding models, which vary); there is no
// batched request shape to speak of. MaxBatchSize() is therefore 1, and
// ai.EmbedMany's batching loop calls Embed once per value as a result.
const embeddingMaxBatchSize = 1

// titanEmbedRequest is the wire request body for Titan's /invoke embedding
// endpoint.
type titanEmbedRequest struct {
	InputText string `json:"inputText"`
}

// titanEmbedResponse is the wire response body for Titan's /invoke
// embedding endpoint.
type titanEmbedResponse struct {
	Embedding           []float64 `json:"embedding"`
	InputTextTokenCount int       `json:"inputTextTokenCount"`
}

type embeddingModel struct {
	provider *Provider
	modelID  string
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Bedrock
// Titan text-embedding model ID (e.g. "amazon.titan-embed-text-v2:0").
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return &embeddingModel{provider: p, modelID: id}
}

func (m *embeddingModel) ModelID() string      { return m.modelID }
func (m *embeddingModel) ProviderName() string { return providerName }
func (m *embeddingModel) MaxBatchSize() int    { return embeddingMaxBatchSize }

// Embed calls Titan's /invoke endpoint with a single input text, per
// Titan's one-text-per-call wire contract. Callers passing more than one
// value get an error rather than a silently truncated or reordered result;
// ai.EmbedMany respects MaxBatchSize() and never does this itself, but a
// direct caller of Embed could.
func (m *embeddingModel) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	if len(values) != 1 {
		return nil, fmt.Errorf("bedrock: Titan embeddings accept exactly one text per call, got %d", len(values))
	}

	reqBody, err := json.Marshal(titanEmbedRequest{InputText: values[0]})
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal embedding request: %w", err)
	}

	path := m.provider.modelPath(m.modelID, "/invoke")
	// Headers is not implemented on the embedding path this wave (nil ->
	// no extra headers).
	resp, err := m.provider.doRequest(ctx, path, reqBody, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bedrock: read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, respBody)
	}

	var wr titanEmbedResponse
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return nil, fmt.Errorf("bedrock: decode embedding response: %w", err)
	}

	return &provider.EmbeddingResponse{
		Embeddings: [][]float64{wr.Embedding},
		Usage: provider.Usage{
			InputTokens: wr.InputTextTokenCount,
			TotalTokens: wr.InputTextTokenCount,
		},
	}, nil
}
