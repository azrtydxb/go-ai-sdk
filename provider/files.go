package provider

import (
	"context"
	"encoding/json"
)

// FileUploadCall is the input to FileStore.UploadFile.
type FileUploadCall struct {
	Data      []byte
	Filename  string
	MediaType string

	// Purpose is provider-specific, e.g. OpenAI's "user_data" (its default
	// when Purpose is empty). Anthropic's Files API has no purpose concept
	// and ignores this field.
	Purpose string

	ProviderOptions map[string]any
}

// FileInfo describes a file that was uploaded to (or otherwise known by) a
// provider's file store.
type FileInfo struct {
	ID        string
	Filename  string
	SizeBytes int64
	MediaType string

	// Raw is the provider's raw JSON response for the upload, for access to
	// fields FileInfo doesn't surface.
	Raw json.RawMessage
}

// FileStore uploads and deletes files with a provider, for later reference
// from a prompt via FilePart.FileID (see provider/message.go).
type FileStore interface {
	UploadFile(ctx context.Context, call FileUploadCall) (*FileInfo, error)
	DeleteFile(ctx context.Context, id string) error
	ProviderName() string
}
