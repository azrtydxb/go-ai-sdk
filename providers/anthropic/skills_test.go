package anthropic

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

type skillsFixture struct {
	uploadAPIKey    string
	uploadVersion   string
	uploadBeta      string
	uploadZip       []byte
	uploadFilename  string
	uploadFormName  string
	uploadDisplayNm string

	deleteMethod string
	deletePath   string
	deleteBeta   string
}

func newSkillsFixture(t *testing.T, uploadStatus int, uploadBody string, deleteStatus int, deleteBody string) (*httptest.Server, *skillsFixture) {
	t.Helper()
	f := &skillsFixture{}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		f.uploadAPIKey = r.Header.Get("x-api-key")
		f.uploadVersion = r.Header.Get("anthropic-version")
		f.uploadBeta = r.Header.Get("anthropic-beta")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				switch part.FormName() {
				case "files[]":
					f.uploadFormName = part.FormName()
					f.uploadFilename = part.FileName()
					f.uploadZip, _ = io.ReadAll(part)
				case "display_name":
					b, _ := io.ReadAll(part)
					f.uploadDisplayNm = string(b)
				}
			}
		}
		w.WriteHeader(uploadStatus)
		w.Write([]byte(uploadBody))
	})
	mux.HandleFunc("/v1/skills/skill-1", func(w http.ResponseWriter, r *http.Request) {
		f.deleteMethod = r.Method
		f.deletePath = r.URL.Path
		f.deleteBeta = r.Header.Get("anthropic-beta")
		w.WriteHeader(deleteStatus)
		w.Write([]byte(deleteBody))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestUploadSkillHappyPath(t *testing.T) {
	srv, f := newSkillsFixture(t, http.StatusOK, `{"id":"skill_1","display_name":"My Skill","version":"1"}`, http.StatusOK, "")
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	info, err := p.UploadSkill(context.Background(), UploadSkillCall{
		Zip:         []byte("zip bytes"),
		DisplayName: "My Skill",
	})
	if err != nil {
		t.Fatalf("UploadSkill: %v", err)
	}
	if f.uploadAPIKey != "k" {
		t.Errorf("x-api-key = %q, want k", f.uploadAPIKey)
	}
	if f.uploadVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", f.uploadVersion, anthropicVersion)
	}
	if f.uploadBeta != skillsBetaHeader {
		t.Errorf("anthropic-beta = %q, want %q", f.uploadBeta, skillsBetaHeader)
	}
	if f.uploadFormName != "files[]" {
		t.Errorf("form field name = %q, want files[]", f.uploadFormName)
	}
	if f.uploadFilename != "skill.zip" {
		t.Errorf("uploaded filename = %q, want skill.zip", f.uploadFilename)
	}
	if string(f.uploadZip) != "zip bytes" {
		t.Errorf("uploaded zip = %q, want zip bytes", f.uploadZip)
	}
	if f.uploadDisplayNm != "My Skill" {
		t.Errorf("display_name field = %q, want My Skill", f.uploadDisplayNm)
	}
	if info.ID != "skill_1" || info.DisplayName != "My Skill" || info.Version != "1" {
		t.Errorf("info = %+v, want {skill_1 My Skill 1 ...}", info)
	}
	if info.Raw == nil {
		t.Error("info.Raw = nil, want set")
	}
}

func TestUploadSkillErrorStatus(t *testing.T) {
	srv, _ := newSkillsFixture(t, http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"invalid api key"}}`, http.StatusOK, "")
	p := New(WithAPIKey("bad"), WithBaseURL(srv.URL))

	_, err := p.UploadSkill(context.Background(), UploadSkillCall{Zip: []byte("z"), DisplayName: "s"})
	if err == nil {
		t.Fatal("UploadSkill: want error for 401 response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *ai.APICallError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
}

func TestUploadSkillRateLimited(t *testing.T) {
	srv, _ := newSkillsFixture(t, http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error","message":"rate limited"}}`, http.StatusOK, "")
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	_, err := p.UploadSkill(context.Background(), UploadSkillCall{Zip: []byte("z"), DisplayName: "s"})
	if err == nil {
		t.Fatal("UploadSkill: want error for 429 response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *ai.APICallError", err)
	}
	if !apiErr.Retryable {
		t.Error("Retryable = false for 429, want true")
	}
}

func TestUploadSkillCtxCanceled(t *testing.T) {
	srv, _ := newSkillsFixture(t, http.StatusOK, `{}`, http.StatusOK, "")
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.UploadSkill(ctx, UploadSkillCall{Zip: []byte("z"), DisplayName: "s"})
	if err == nil {
		t.Fatal("UploadSkill: want error for canceled context")
	}
}

func TestDeleteSkillHappyPath(t *testing.T) {
	srv, f := newSkillsFixture(t, http.StatusOK, "", http.StatusOK, `{"id":"skill_1","deleted":true}`)
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	err := p.DeleteSkill(context.Background(), "skill-1")
	if err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if f.deleteMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", f.deleteMethod)
	}
	if f.deletePath != "/v1/skills/skill-1" {
		t.Errorf("path = %q, want /v1/skills/skill-1", f.deletePath)
	}
	if f.deleteBeta != skillsBetaHeader {
		t.Errorf("anthropic-beta = %q, want %q", f.deleteBeta, skillsBetaHeader)
	}
}

func TestDeleteSkillErrorStatus(t *testing.T) {
	srv, _ := newSkillsFixture(t, http.StatusOK, "", http.StatusNotFound, `{"error":{"type":"not_found_error","message":"not found"}}`)
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	err := p.DeleteSkill(context.Background(), "skill-1")
	if err == nil {
		t.Fatal("DeleteSkill: want error for 404 response")
	}
}

// --- Security: multipart CRLF/quote injection guard ---
//
// mime/multipart.Writer writes MIME headers verbatim with no CRLF
// validation (unlike net/http), so a caller-supplied DisplayName or
// ProviderOptions key/value containing "\r\n" could otherwise forge extra
// multipart headers or parts. These tests confirm such values are
// rejected before anything is sent to the server.

func TestUploadSkillDisplayNameCRLFRejected(t *testing.T) {
	var hit bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	_, err := p.UploadSkill(context.Background(), UploadSkillCall{
		Zip:         []byte("z"),
		DisplayName: "My Skill\r\nX-Injected: 1",
	})
	if err == nil {
		t.Fatal("UploadSkill: want error for DisplayName containing CRLF")
	}
	if hit {
		t.Error("UploadSkill: request was sent despite invalid DisplayName")
	}
}

func TestUploadSkillProviderOptionKeyNewlineRejected(t *testing.T) {
	var hit bool
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))

	_, err := p.UploadSkill(context.Background(), UploadSkillCall{
		Zip:         []byte("z"),
		DisplayName: "s",
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"evil\nX-Injected: 1": "v",
			},
		},
	})
	if err == nil {
		t.Fatal("UploadSkill: want error for ProviderOptions key containing LF")
	}
	if hit {
		t.Error("UploadSkill: request was sent despite invalid provider option key")
	}
}
