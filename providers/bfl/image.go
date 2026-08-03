package bfl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchimage"
	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// imageModel implements provider.ImageModel against Black Forest Labs'
// asynchronous image-generation endpoint: a generation is created, then
// polled at the absolute polling_url it returns until it reaches a
// terminal state.
type imageModel struct {
	provider *Provider
	modelID  string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return providerName }

// ---- wire types ----

type createRequest struct {
	Prompt string `json:"prompt"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type createResponse struct {
	ID         string `json:"id"`
	PollingURL string `json:"polling_url"`
}

// pollResponse matches BFL's polling response shape, returned by GETting
// the absolute polling_url from createResponse.
type pollResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Result struct {
		Sample string `json:"sample"`
	} `json:"result"`
}

// failureStatuses are terminal BFL statuses that indicate the generation
// did not succeed.
var failureStatuses = map[string]bool{
	"Error":             true,
	"Content Moderated": true,
	"Request Moderated": true,
	"Task not found":    true,
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	req := createRequest{Prompt: call.Prompt}
	if call.Size != "" {
		w, h, ok := strings.Cut(call.Size, "x")
		if ok {
			if width, err := strconv.Atoi(w); err == nil {
				req.Width = width
			}
			if height, err := strconv.Atoi(h); err == nil {
				req.Height = height
			}
		}
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("bfl: marshal image request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("bfl: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/" + m.modelID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("bfl: build image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("bfl: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var created createResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, fmt.Errorf("bfl: decode image response: %w", err)
	}
	if created.PollingURL == "" {
		return nil, fmt.Errorf("bfl: response contained no polling_url: %s", body)
	}

	poll, rawBody, err := m.poll(ctx, created.PollingURL)
	if err != nil {
		return nil, err
	}

	if poll.Result.Sample == "" {
		return nil, fmt.Errorf("bfl: ready generation contained no sample url: %s", rawBody)
	}

	data, mediaType, err := fetchimage.Fetch(ctx, m.provider.client(), poll.Result.Sample, "bfl")
	if err != nil {
		return nil, err
	}

	return &provider.ImageResponse{
		Images: []provider.GeneratedImage{{Data: data, MediaType: mediaType}},
		Raw:    json.RawMessage(rawBody),
	}, nil
}

// maxPollBodyBytes caps how much of a poll response body is read into
// memory. BFL's poll responses are small status/result JSON objects, so
// this is generous headroom rather than a tight fit -- it exists purely as
// a memory-DoS backstop against a compromised or malicious polling_url
// returning an unbounded body.
const maxPollBodyBytes = 1 << 20 // 1MB

// poll repeatedly GETs the absolute pollingURL until the generation
// reaches a terminal state ("Ready" or a failure status), sleeping
// p.provider.poll() between requests. The sleep is ctx-aware: cancellation
// returns ctx.Err() immediately instead of waiting out the interval.
//
// pollingURL is chosen by the server's create-call response, not by the
// caller -- so before ever attaching the x-key credential to a request,
// poll requires pollingURL to share the configured base URL's registrable
// domain (fetchmedia.SameRegistrableDomain; BFL returns region-specific
// polling hosts like api.us1.bfl.ai/api.eu1.bfl.ai alongside the
// api.bfl.ai base, so an exact host match would reject every real
// generation -- see SameRegistrableDomain's doc for the heuristic and its
// limitations), and validates it against SSRF targets
// (fetchmedia.ValidateURL: link-local/metadata addresses, including cloud
// metadata at 169.254.169.254). A mismatch or rejected URL fails closed
// before any request -- with credentials -- is ever sent to it.
//
// Redirects are refused outright. The Transport is wrapped with
// fetchmedia.PinnedTransport so the SSRF check also holds at actual
// dial time, not just at this pre-connect validation (see PinnedTransport
// for why a pre-connect-only check is vulnerable to DNS-rebind). The poll
// response body is also capped (maxPollBodyBytes) as a memory-DoS
// backstop.
func (m *imageModel) poll(ctx context.Context, pollingURL string) (*pollResponse, []byte, error) {
	if !fetchmedia.SameRegistrableDomain(m.provider.baseURL, pollingURL) {
		return nil, nil, fmt.Errorf("bfl: polling_url %q is not on the same registrable domain as the configured base URL %q; refusing to send the API key to it", pollingURL, m.provider.baseURL)
	}
	if err := fetchmedia.ValidateURL(ctx, pollingURL); err != nil {
		return nil, nil, fmt.Errorf("bfl: polling_url %q rejected: %w", pollingURL, err)
	}

	base := m.provider.client()
	pollClient := &http.Client{
		Transport: fetchmedia.PinnedTransport(base.Transport),
		Jar:       base.Jar,
		Timeout:   base.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("bfl: refusing to follow redirect while polling %q", pollingURL)
		},
	}

	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollingURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("bfl: build poll request: %w", err)
		}
		httpReq.Header.Set("x-key", m.provider.apiKey)

		resp, err := pollClient.Do(httpReq)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxPollBodyBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("bfl: read poll response: %w", err)
		}
		if len(body) > maxPollBodyBytes {
			return nil, nil, fmt.Errorf("bfl: poll response body exceeds %d bytes", maxPollBodyBytes)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, apiError(resp, body)
		}

		var poll pollResponse
		if err := json.Unmarshal(body, &poll); err != nil {
			return nil, nil, fmt.Errorf("bfl: decode poll response: %w", err)
		}

		if poll.Status == "Ready" {
			return &poll, body, nil
		}
		if failureStatuses[poll.Status] {
			return nil, nil, fmt.Errorf("bfl: generation %s failed with status %q: %s", poll.ID, poll.Status, body)
		}

		if err := sleep(ctx, m.provider.poll()); err != nil {
			return nil, nil, err
		}
	}
}

// sleep blocks for d or until ctx is done, whichever comes first,
// returning ctx.Err() in the latter case.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
