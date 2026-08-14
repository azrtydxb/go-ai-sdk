package voyage

// ---- Embeddings wire types ----

type embeddingRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	InputType string   `json:"input_type,omitempty"`
}

type embeddingResponse struct {
	Data  []embeddingDataWire `json:"data"`
	Usage embeddingUsageWire  `json:"usage"`
}

type embeddingDataWire struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingUsageWire struct {
	TotalTokens int `json:"total_tokens"`
}

// ---- Rerank wire types ----

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopK      *int     `json:"top_k,omitempty"`
}

type rerankResponse struct {
	Data  []rerankResultWire `json:"data"`
	Usage rerankUsageWire    `json:"usage"`
}

type rerankResultWire struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankUsageWire struct {
	TotalTokens int `json:"total_tokens"`
}
