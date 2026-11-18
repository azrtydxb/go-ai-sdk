package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// maxListBytes bounds a /models or key-info response. OpenRouter's full
// model list is a few MiB.
const maxListBytes = 32 << 20

// NewModelLister returns a provider.ModelLister that reads the OpenAI
// GET {base}/models list against cfg. It understands the plain OpenAI shape
// ({data:[{id, created, owned_by}]}) and the extras OpenRouter, vLLM and the
// Vercel AI Gateway add (name, description, context length, architecture
// modalities, supported_voices).
//
// modalityParam names the query parameter that carries
// ModelListCall.OutputModality to the server ("output_modalities" for
// OpenRouter); "" sends none. Either way the filter is also applied to the
// decoded list.
func NewModelLister(cfg Config, modalityParam string) provider.ModelLister {
	return &modelLister{cfg: cfg, modalityParam: modalityParam}
}

type modelLister struct {
	cfg           Config
	modalityParam string
}

func (l *modelLister) ProviderName() string { return l.cfg.Name }

// ---- wire types ----

type modelListWire struct {
	Data []json.RawMessage `json:"data"`
}

type modelWire struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	OwnedBy       string `json:"owned_by"`
	Created       any    `json:"created"`
	ContextLength any    `json:"context_length"` // OpenRouter
	MaxModelLen   any    `json:"max_model_len"`  // vLLM
	ContextWindow any    `json:"context_window"` // Vercel AI Gateway
	Architecture  struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	SupportedVoices []string `json:"supported_voices"`
}

// wholeNumber reads a JSON number (or a numeric string) as an int64; any
// other value is 0. Servers disagree on these fields' types, and one odd
// value must not fail the whole list.
func wholeNumber(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case string:
		f, err := json.Number(n).Float64()
		if err != nil {
			return 0
		}
		return int64(f)
	}
	return 0
}

func (w modelWire) info(raw json.RawMessage) provider.ModelInfo {
	mi := provider.ModelInfo{
		ID:               w.ID,
		Name:             w.Name,
		Description:      w.Description,
		OwnedBy:          w.OwnedBy,
		InputModalities:  w.Architecture.InputModalities,
		OutputModalities: w.Architecture.OutputModalities,
		Voices:           w.SupportedVoices,
		Raw:              raw,
	}
	if s := wholeNumber(w.Created); s > 0 {
		mi.Created = time.Unix(s, 0).UTC()
	}
	for _, v := range []any{w.ContextLength, w.MaxModelLen, w.ContextWindow} {
		if n := wholeNumber(v); n > 0 {
			mi.ContextLength = int(n)
			break
		}
	}
	return mi
}

// hasModality reports whether a model with the given modalities passes a
// filter for want: no filter, a model that reports nothing, or a match.
func hasModality(have []string, want string) bool {
	return want == "" || len(have) == 0 || slices.Contains(have, want)
}

// ListModels implements provider.ModelLister.
func (l *modelLister) ListModels(ctx context.Context, call provider.ModelListCall) ([]provider.ModelInfo, error) {
	q := url.Values{}
	if call.OutputModality != "" && l.modalityParam != "" {
		q.Set(l.modalityParam, call.OutputModality)
	}
	body, err := l.cfg.get(ctx, "/models", q, call.Headers)
	if err != nil {
		return nil, err
	}
	var list modelListWire
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("openaicompat: decode model list: %w", err)
	}
	out := make([]provider.ModelInfo, 0, len(list.Data))
	for _, raw := range list.Data {
		var w modelWire
		if err := json.Unmarshal(raw, &w); err != nil {
			return nil, fmt.Errorf("openaicompat: decode model: %w", err)
		}
		if w.ID == "" {
			continue
		}
		mi := w.info(raw)
		if call.OutputModality != "all" && !hasModality(mi.OutputModalities, call.OutputModality) {
			continue
		}
		if !hasModality(mi.InputModalities, call.InputModality) {
			continue
		}
		out = append(out, mi)
	}
	return out, nil
}

// get makes an authenticated GET {base}{path}?{q} and returns the body of a
// 2xx answer; any other status is an *ai.APICallError.
func (c Config) get(ctx context.Context, path string, q url.Values, headers map[string]string) ([]byte, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", c.Name)
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.setAuthHeader(req)
	httpheader.Apply(req, headers, c.authHeaderName())

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}
	if len(body) > maxListBytes {
		return nil, fmt.Errorf("openaicompat: response from %s exceeds %d bytes", path, maxListBytes)
	}
	return body, nil
}

// NewKeyChecker returns a provider.KeyChecker against cfg. When keyInfoPath
// is set (OpenRouter: "/key") it reads that key-info endpoint, expecting
// OpenRouter's {data:{label, limit, usage, limit_remaining, is_free_tier}}
// shape. When keyInfoPath is empty, or the server answers it with 404 or
// 405 or with a body not in that shape, the key is checked by an
// authenticated GET {base}/models instead and the KeyInfo is not Detailed.
func NewKeyChecker(cfg Config, keyInfoPath string) provider.KeyChecker {
	return &keyChecker{cfg: cfg, path: keyInfoPath}
}

type keyChecker struct {
	cfg  Config
	path string
}

func (k *keyChecker) ProviderName() string { return k.cfg.Name }

type keyInfoWire struct {
	Data *struct {
		Label          string   `json:"label"`
		Limit          *float64 `json:"limit"`
		Usage          *float64 `json:"usage"`
		LimitRemaining *float64 `json:"limit_remaining"`
		IsFreeTier     bool     `json:"is_free_tier"`
	} `json:"data"`
}

// CheckKey implements provider.KeyChecker.
func (k *keyChecker) CheckKey(ctx context.Context, call provider.KeyCheckCall) (*provider.KeyInfo, error) {
	if k.path != "" {
		body, err := k.cfg.get(ctx, k.path, nil, call.Headers)
		switch {
		case err == nil:
			if info, ok := decodeKeyInfo(body); ok {
				return info, nil
			}
		case !noSuchEndpoint(err):
			return nil, err
		}
	}
	if _, err := k.cfg.get(ctx, "/models", nil, call.Headers); err != nil {
		return nil, err
	}
	return &provider.KeyInfo{}, nil
}

func decodeKeyInfo(body []byte) (*provider.KeyInfo, bool) {
	var w keyInfoWire
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return nil, false
	}
	return &provider.KeyInfo{
		Detailed:       true,
		Label:          w.Data.Label,
		Limit:          w.Data.Limit,
		Usage:          w.Data.Usage,
		LimitRemaining: w.Data.LimitRemaining,
		FreeTier:       w.Data.IsFreeTier,
		Raw:            json.RawMessage(bytes.Clone(body)),
	}, true
}

// noSuchEndpoint reports whether err is the server saying the key-info
// path does not exist, as opposed to rejecting the key.
func noSuchEndpoint(err error) bool {
	var api *ai.APICallError
	if !errors.As(err, &api) {
		return false
	}
	return api.StatusCode == http.StatusNotFound || api.StatusCode == http.StatusMethodNotAllowed
}
