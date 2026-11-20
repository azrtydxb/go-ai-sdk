package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ClassifyShape selects the wire format of a sequence-classification
// endpoint.
type ClassifyShape int

const (
	// ClassifyOpenAI is vLLM's classification API: POST {base}/classify with
	// {model, input:[...]} returning
	// {data:[{index, label, probs, num_classes}], usage}. vLLM serves
	// /classify at the server root, so the base URL has no /v1 suffix (or
	// use WithClassifyBaseURL when the provider's base URL ends in /v1).
	// The response carries the probability of every class but the name of
	// only the winning one; pass WithClassifyLabels to name them all.
	ClassifyOpenAI ClassifyShape = iota
	// ClassifyTEI is the Hugging Face Text Embeddings Inference shape:
	// POST {base}/predict with {inputs:[[text],...]} returning, per input,
	// [{label, score}] for every class. TEI serves /predict at its root, so
	// the base URL has no /v1 suffix and the model is fixed by the server
	// (the model id is informational). The model must be a sequence
	// classification model, else TEI answers 424.
	ClassifyTEI
)

// ClassifyOption configures a classification model.
type ClassifyOption func(*classificationModel)

// WithClassifyShape selects the wire shape (default ClassifyOpenAI).
func WithClassifyShape(s ClassifyShape) ClassifyOption {
	return func(m *classificationModel) { m.shape = s }
}

// WithClassifyLabels names the classes of the model by output index
// (id2label order), so every provider.LabelScore of a ClassifyOpenAI
// result carries its label, not only the winner. A response with a
// different class count than len(labels) is an error. Unused by
// ClassifyTEI, whose responses already name every class.
func WithClassifyLabels(labels []string) ClassifyOption {
	return func(m *classificationModel) { m.labels = append([]string(nil), labels...) }
}

// WithClassifyBaseURL overrides the provider's base URL for this model's
// requests, for servers that serve the classification endpoint at a
// different root than the OpenAI-style /v1 paths (vLLM serves /classify at
// the root while chat lives under /v1).
func WithClassifyBaseURL(baseURL string) ClassifyOption {
	return func(m *classificationModel) { m.baseURL = strings.TrimRight(baseURL, "/") }
}

// WithClassifyRawScores asks a ClassifyTEI server for raw logits instead of
// the model's softmax/sigmoid output ("raw_scores":true). It has no effect
// on ClassifyOpenAI; vLLM's equivalent is ProviderOptions
// {"use_activation": false}.
func WithClassifyRawScores() ClassifyOption {
	return func(m *classificationModel) { m.rawScores = true }
}

// Classify returns a provider.ClassificationModel for modelID, for
// ai.Classify. Extra server parameters (vLLM use_activation and
// truncate_prompt_tokens, TEI truncate and truncation_direction) go through
// ClassifyCall.ProviderOptions under this provider's name.
func (p *Provider) Classify(modelID string, opts ...ClassifyOption) provider.ClassificationModel {
	m := &classificationModel{p: p, modelID: modelID}
	for _, o := range opts {
		o(m)
	}
	return m
}

type classificationModel struct {
	p         *Provider
	modelID   string
	shape     ClassifyShape
	labels    []string
	baseURL   string
	rawScores bool
}

func (m *classificationModel) ModelID() string      { return m.modelID }
func (m *classificationModel) ProviderName() string { return m.p.name }

type vllmClassifyRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type vllmClassifyResponse struct {
	Data []struct {
		Index      int       `json:"index"`
		Label      *string   `json:"label"`
		Probs      []float64 `json:"probs"`
		NumClasses int       `json:"num_classes"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// teiPredictRequest sends every input as a one-element array: TEI reads a
// bare array of two strings as a (text, text-pair) input, whereas an array
// of arrays is a batch of independent inputs.
type teiPredictRequest struct {
	Inputs    [][]string `json:"inputs"`
	RawScores bool       `json:"raw_scores,omitempty"`
}

type teiPrediction struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

func (m *classificationModel) Classify(ctx context.Context, call provider.ClassifyCall) (*provider.ClassifyResponse, error) {
	p := m.p
	base := m.baseURL
	if base == "" {
		base = p.baseURL
	}
	if base == "" {
		return nil, fmt.Errorf("%s: base URL not configured", p.name)
	}
	if len(call.Inputs) == 0 {
		return nil, fmt.Errorf("%s: classify: no inputs", p.name)
	}

	var (
		reqBody []byte
		path    string
		err     error
	)
	switch m.shape {
	case ClassifyOpenAI:
		path = "/classify"
		reqBody, err = json.Marshal(vllmClassifyRequest{Model: m.modelID, Input: call.Inputs})
	case ClassifyTEI:
		path = "/predict"
		batch := make([][]string, len(call.Inputs))
		for i, in := range call.Inputs {
			batch[i] = []string{in}
		}
		reqBody, err = json.Marshal(teiPredictRequest{Inputs: batch, RawScores: m.rawScores})
	default:
		return nil, fmt.Errorf("%s: unknown classify shape %d", p.name, m.shape)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: marshal classify request: %w", p.name, err)
	}
	reqBody, err = providerutil.ApplyProviderOptions(reqBody, call.ProviderOptions, p.name)
	if err != nil {
		return nil, fmt.Errorf("%s: apply provider options: %w", p.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("%s: build classify request: %w", p.name, err)
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
		return nil, fmt.Errorf("%s: read classify response: %w", p.name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ai.NewAPICallErrorFromResponse(resp, string(body), providerutil.ErrorMessage(body))
	}

	out := &provider.ClassifyResponse{Raw: json.RawMessage(body)}
	switch m.shape {
	case ClassifyOpenAI:
		var wr vllmClassifyResponse
		if err := json.Unmarshal(body, &wr); err != nil {
			return nil, fmt.Errorf("%s: decode classify response: %w", p.name, err)
		}
		results, err := m.vllmResults(wr, len(call.Inputs))
		if err != nil {
			return nil, err
		}
		out.Results = results
		out.Usage = provider.Usage{InputTokens: wr.Usage.PromptTokens, TotalTokens: wr.Usage.TotalTokens}
	case ClassifyTEI:
		var batches [][]teiPrediction
		if err := json.Unmarshal(body, &batches); err != nil {
			return nil, fmt.Errorf("%s: decode classify response: %w", p.name, err)
		}
		if len(batches) != len(call.Inputs) {
			return nil, fmt.Errorf("%s: classify response has %d results for %d inputs", p.name, len(batches), len(call.Inputs))
		}
		for i, preds := range batches {
			c := provider.Classification{Index: i}
			for _, pr := range preds {
				c.Scores = append(c.Scores, provider.LabelScore{Index: -1, Label: pr.Label, Score: pr.Score})
			}
			finishClassification(&c)
			out.Results = append(out.Results, c)
		}
	}
	return out, nil
}

// vllmResults converts vLLM's data[] (matched to the inputs by "index")
// into Classifications, naming classes from the configured labels and, for
// the winning class, from the response's own label.
func (m *classificationModel) vllmResults(wr vllmClassifyResponse, n int) ([]provider.Classification, error) {
	name := m.p.name
	if len(wr.Data) != n {
		return nil, fmt.Errorf("%s: classify response has %d results for %d inputs", name, len(wr.Data), n)
	}
	results := make([]provider.Classification, n)
	seen := make([]bool, n)
	for _, d := range wr.Data {
		if d.Index < 0 || d.Index >= n || seen[d.Index] {
			return nil, fmt.Errorf("%s: classify response has invalid or duplicate index %d", name, d.Index)
		}
		seen[d.Index] = true
		if len(m.labels) > 0 && len(m.labels) != len(d.Probs) {
			return nil, fmt.Errorf("%s: classify response has %d classes but %d labels are configured", name, len(d.Probs), len(m.labels))
		}
		c := provider.Classification{Index: d.Index}
		best := 0
		for i, pr := range d.Probs {
			ls := provider.LabelScore{Index: i, Score: pr}
			if len(m.labels) > 0 {
				ls.Label = m.labels[i]
			}
			if pr > d.Probs[best] {
				best = i
			}
			c.Scores = append(c.Scores, ls)
		}
		if d.Label != nil && len(d.Probs) > 0 && c.Scores[best].Label == "" {
			// The response names only the winning class.
			c.Scores[best].Label = *d.Label
		}
		finishClassification(&c)
		results[d.Index] = c
	}
	return results, nil
}

// finishClassification sorts Scores highest first (stable, so ties keep
// the server's order) and sets Label to the winner's.
func finishClassification(c *provider.Classification) {
	sort.SliceStable(c.Scores, func(i, j int) bool { return c.Scores[i].Score > c.Scores[j].Score })
	if len(c.Scores) > 0 {
		c.Label = c.Scores[0].Label
	}
}
