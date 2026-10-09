package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// RerankShape selects the wire format of a rerank endpoint.
type RerankShape int

const (
	// RerankOpenAI is the vLLM / OpenAI-compatible (Jina/Cohere-like) shape:
	// POST {base}/rerank with {model, query, documents, top_n} returning
	// {results:[{index, relevance_score}]}. With vLLM, base normally ends
	// in /v1.
	RerankOpenAI RerankShape = iota
	// RerankTEI is the Hugging Face Text Embeddings Inference shape:
	// POST {base}/rerank with {query, texts} returning [{index, score}].
	// TEI serves /rerank at its root, so the base URL has no /v1 suffix and
	// the model is fixed by the server (the model id is informational).
	RerankTEI
)

// RerankOption configures a rerank model.
type RerankOption func(*rerankingModel)

// WithRerankShape selects the wire shape (default RerankOpenAI).
func WithRerankShape(s RerankShape) RerankOption { return func(m *rerankingModel) { m.shape = s } }

// Rerank returns a provider.RerankingModel for modelID. Results are sorted
// by score, highest first.
func (p *Provider) Rerank(modelID string, opts ...RerankOption) provider.RerankingModel {
	m := &rerankingModel{p: p, modelID: modelID}
	for _, o := range opts {
		o(m)
	}
	return m
}

type rerankingModel struct {
	p       *Provider
	modelID string
	shape   RerankShape
}

func (m *rerankingModel) ModelID() string      { return m.modelID }
func (m *rerankingModel) ProviderName() string { return m.p.name }

type openAIRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      *int     `json:"top_n,omitempty"`
}

type openAIRerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"results"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

type teiRerankRequest struct {
	Query string   `json:"query"`
	Texts []string `json:"texts"`
}

type teiRerankItem struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

func (m *rerankingModel) Rerank(ctx context.Context, call provider.RerankCall) (*provider.RerankResponse, error) {
	p := m.p
	if p.baseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", p.name)
	}

	var (
		reqBody []byte
		err     error
	)
	switch m.shape {
	case RerankOpenAI:
		req := openAIRerankRequest{Model: m.modelID, Query: call.Query, Documents: call.Documents}
		if call.TopN > 0 {
			n := call.TopN
			req.TopN = &n
		}
		reqBody, err = json.Marshal(req)
	case RerankTEI:
		reqBody, err = json.Marshal(teiRerankRequest{Query: call.Query, Texts: call.Documents})
	default:
		return nil, fmt.Errorf("%s: unknown rerank shape %d", p.name, m.shape)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: marshal rerank request: %w", p.name, err)
	}
	reqBody, err = providerutil.ApplyProviderOptions(reqBody, call.ProviderOptions, p.name)
	if err != nil {
		return nil, fmt.Errorf("%s: apply provider options: %w", p.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/rerank", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("%s: build rerank request: %w", p.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	httpheader.Apply(httpReq, p.headers, "Authorization")
	httpheader.Apply(httpReq, call.Headers, "Authorization")

	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read rerank response: %w", p.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ai.NewAPICallErrorFromResponse(resp, string(body), providerutil.ErrorMessage(body))
	}

	out := &provider.RerankResponse{Raw: json.RawMessage(body)}
	switch m.shape {
	case RerankOpenAI:
		var wr openAIRerankResponse
		if err := json.Unmarshal(body, &wr); err != nil {
			return nil, fmt.Errorf("%s: decode rerank response: %w", p.name, err)
		}
		for _, r := range wr.Results {
			out.Results = append(out.Results, provider.RankedDocument{Index: r.Index, Score: r.RelevanceScore})
		}
		out.Usage = provider.Usage{TotalTokens: wr.Usage.TotalTokens}
	case RerankTEI:
		var items []teiRerankItem
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("%s: decode rerank response: %w", p.name, err)
		}
		for _, r := range items {
			out.Results = append(out.Results, provider.RankedDocument{Index: r.Index, Score: r.Score})
		}
	}

	sort.SliceStable(out.Results, func(i, j int) bool { return out.Results[i].Score > out.Results[j].Score })
	// TEI has no top_n; vLLM servers may ignore it. Enforce it here.
	if call.TopN > 0 && len(out.Results) > call.TopN {
		out.Results = out.Results[:call.TopN]
	}
	return out, nil
}
