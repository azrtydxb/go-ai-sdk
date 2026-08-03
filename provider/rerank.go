package provider

import (
	"context"
	"encoding/json"
)

// RerankCall is a request to rank Documents by relevance to Query.
type RerankCall struct {
	Query           string
	Documents       []string
	TopN            int // 0 = provider default (all documents)
	ProviderOptions map[string]any
}

// RankedDocument is one scored entry in a rerank response. Index refers to
// the position in RerankCall.Documents.
type RankedDocument struct {
	Index int
	Score float64
}

// RerankResponse is the outcome of a RerankingModel.Rerank call.
type RerankResponse struct {
	Results []RankedDocument // sorted most-relevant first, as returned by the provider
	Usage   Usage
	Raw     json.RawMessage
}

// RerankingModel is implemented by providers that can rank a set of
// documents by relevance to a query.
type RerankingModel interface {
	Rerank(ctx context.Context, call RerankCall) (*RerankResponse, error)
	ModelID() string
	ProviderName() string
}
