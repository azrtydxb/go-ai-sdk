// skills.go implements Anthropic's Skills API
// (https://api.anthropic.com/v1/skills), a provider-specific capability with
// no generic provider interface (unlike Files, which implements
// provider.FileStore). Callers use (*Provider).UploadSkill and
// (*Provider).DeleteSkill directly.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/multipartutil"
)

// skillsBetaHeader is the anthropic-beta header value required by the
// Skills API. It is set only on skills.go's own requests — never on the
// shared language-model path (providers/anthropic/language_model.go).
const skillsBetaHeader = "skills-2025-10-02"

// SkillInfo describes a skill uploaded to (or otherwise known by)
// Anthropic's Skills API.
type SkillInfo struct {
	ID          string
	DisplayName string
	Version     string

	// Raw is the provider's raw JSON response, for access to fields
	// SkillInfo doesn't surface.
	Raw json.RawMessage
}

// UploadSkillCall is the input to (*Provider).UploadSkill.
type UploadSkillCall struct {
	Zip             []byte
	DisplayName     string
	ProviderOptions map[string]any
}

type skillWireResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
}

// UploadSkill uploads a skill.zip to Anthropic's Skills API. It POSTs a
// multipart request to {base}/v1/skills with the file part named "files[]"
// (filename "skill.zip") and a "display_name" field, sending the
// x-api-key, anthropic-version, and anthropic-beta: skills-2025-10-02
// headers.
func (p *Provider) UploadSkill(ctx context.Context, call UploadSkillCall) (*SkillInfo, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("files[]", "skill.zip")
	if err != nil {
		return nil, fmt.Errorf("anthropic: create skill file part: %w", err)
	}
	if _, err := fw.Write(call.Zip); err != nil {
		return nil, fmt.Errorf("anthropic: write skill file part: %w", err)
	}

	if err := multipartutil.ValidField("display_name", call.DisplayName); err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if err := mw.WriteField("display_name", call.DisplayName); err != nil {
		return nil, fmt.Errorf("anthropic: write display_name field: %w", err)
	}

	if opts, ok := call.ProviderOptions["anthropic"].(map[string]any); ok {
		for k, v := range opts {
			if err := multipartutil.ValidField("provider option field name", k); err != nil {
				return nil, fmt.Errorf("anthropic: %w", err)
			}
			sv := fmt.Sprint(v)
			if err := multipartutil.ValidField("provider option field value", sv); err != nil {
				return nil, fmt.Errorf("anthropic: %w", err)
			}
			if err := mw.WriteField(k, sv); err != nil {
				return nil, fmt.Errorf("anthropic: write provider option field %q: %w", k, err)
			}
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("anthropic: close multipart writer: %w", err)
	}

	url := p.baseURL + "/v1/skills"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build skill upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set(anthropicAuthHeader, p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-beta", skillsBetaHeader)

	resp, err := p.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read skill upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr skillWireResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("anthropic: decode skill upload response: %w", err)
	}

	return &SkillInfo{
		ID:          wr.ID,
		DisplayName: wr.DisplayName,
		Version:     wr.Version,
		Raw:         json.RawMessage(body),
	}, nil
}

// DeleteSkill deletes a previously-uploaded skill. It DELETEs
// {base}/v1/skills/{id} with the same anthropic-beta header as
// UploadSkill.
func (p *Provider) DeleteSkill(ctx context.Context, id string) error {
	url := p.baseURL + "/v1/skills/" + id
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("anthropic: build skill delete request: %w", err)
	}
	httpReq.Header.Set(anthropicAuthHeader, p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-beta", skillsBetaHeader)

	resp, err := p.client().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("anthropic: read skill delete response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp, body)
	}

	return nil
}
