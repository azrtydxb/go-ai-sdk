// skills.go implements Anthropic's Skills API
// (https://api.anthropic.com/v1/skills), a provider-specific capability with
// no generic provider interface (unlike Files, which implements
// provider.FileStore). Callers use (*Provider).UploadSkill and
// (*Provider).DeleteSkill directly.
package anthropic

import (
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
	var wr skillWireResponse
	body, err := p.postMultipart(ctx, "/v1/skills", skillsBetaHeader, nil, func(mw *multipart.Writer) error {
		fw, err := mw.CreateFormFile("files[]", "skill.zip")
		if err != nil {
			return fmt.Errorf("create skill file part: %w", err)
		}
		if _, err := fw.Write(call.Zip); err != nil {
			return fmt.Errorf("write skill file part: %w", err)
		}
		if err := multipartutil.ValidField("display_name", call.DisplayName); err != nil {
			return err
		}
		if err := mw.WriteField("display_name", call.DisplayName); err != nil {
			return fmt.Errorf("write display_name field: %w", err)
		}
		return multipartutil.ApplyProviderOptionsForm(mw, call.ProviderOptions, "anthropic")
	}, &wr)
	if err != nil {
		return nil, err
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
