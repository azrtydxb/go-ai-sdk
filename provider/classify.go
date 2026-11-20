package provider

import (
	"context"
	"encoding/json"
)

// ClassifyCall is a request to score each of Inputs with a
// sequence-classification model (for example a prompt-injection or toxicity
// classifier such as Llama Prompt Guard).
type ClassifyCall struct {
	Inputs          []string
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
}

// LabelScore is one class's score for one input.
type LabelScore struct {
	// Index is the class index in the model's output, or -1 when the
	// server does not report it (Hugging Face TEI).
	Index int
	// Label is the class name. Servers that report only the winning
	// label (vLLM) leave it empty for every other class unless the model
	// was configured with the class names.
	Label string
	// Score is the probability the server reports (after the model's
	// activation, softmax or sigmoid), unless raw scores were requested.
	Score float64
}

// Classification is the result for one input.
type Classification struct {
	// Index is the position of the input in ClassifyCall.Inputs.
	Index int
	// Label is the winning (highest score) label, or "" when unknown.
	Label string
	// Scores lists every class, highest score first.
	Scores []LabelScore
}

// Score returns the score of the class named label.
func (c Classification) Score(label string) (float64, bool) {
	for _, s := range c.Scores {
		if s.Label == label {
			return s.Score, true
		}
	}
	return 0, false
}

// ClassifyResponse is the outcome of a ClassificationModel.Classify call.
type ClassifyResponse struct {
	Results []Classification // one per input, in input order
	Usage   Usage
	Raw     json.RawMessage
}

// ClassificationModel is implemented by providers that can score text with
// a sequence-classification model.
type ClassificationModel interface {
	Classify(ctx context.Context, call ClassifyCall) (*ClassifyResponse, error)
	ModelID() string
	ProviderName() string
}
