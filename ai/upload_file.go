package ai

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrStoreRequired is returned when Store is nil in UploadFileOpts or
// DeleteFileOpts.
var ErrStoreRequired = errors.New("ai: store is required")

// ErrDataRequired is returned when Data is empty in UploadFileOpts.
var ErrDataRequired = errors.New("ai: data is required")

// ErrFilenameRequired is returned when Filename is empty in UploadFileOpts.
var ErrFilenameRequired = errors.New("ai: filename is required")

// ErrIDRequired is returned when ID is empty in DeleteFileOpts.
var ErrIDRequired = errors.New("ai: id is required")

// ErrDataTooLarge is returned when Data exceeds UploadFileOpts.MaxBytes.
var ErrDataTooLarge = errors.New("ai: data exceeds MaxBytes")

// ErrMediaTypeForbidden is returned when MediaType is set and does not pass
// the Allowlist filter (when UploadFileOpts.MediaTypeAllowlist is set).
var ErrMediaTypeForbidden = errors.New("ai: media type not in allowlist")

// maxUploadBytes is the default ceiling when UploadFileOpts.MaxBytes is zero.
const maxUploadBytes int64 = 256 << 20 // 256 MiB

// sanitizeMediaType lowercases the type and subtype of mt and strips any
// parameters (e.g. "; charset=binary") so that allowlist matching is
// case-insensitive and parameter-agnostic. Returns "" on parse failure.
func sanitizeMediaType(mt string) string {
	base, _, err := mime.ParseMediaType(mt)
	if err != nil {
		return ""
	}
	return strings.ToLower(base)
}

// UploadFileOpts options for the UploadFile function.
type UploadFileOpts struct {
	Store           provider.FileStore // required
	Data            []byte             // required
	Filename        string             // required
	MediaType       string
	Purpose         string
	MaxRetries      *int
	ProviderOptions map[string]any
	// MaxBytes is the ceiling on Data; zero means maxUploadBytes (256 MiB).
	MaxBytes int64
	// MediaTypeAllowlist, when non-empty, restricts uploads to the listed
	// media types (case-insensitive, parameters stripped). An empty allowlist
	// means all media types are permitted.
	MediaTypeAllowlist []string

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.FileUploadCall.Headers unchanged — see that
	// field's doc for precedence (it never overrides the provider's auth
	// header) and which request paths implement it.
	Headers map[string]string
}

// UploadFile uploads a file to the given provider.FileStore. It wraps the
// call in retry logic (default maxRetries = 2). The returned *provider.FileInfo's
// ID can be referenced from a later prompt via provider.FilePart.FileID.
func UploadFile(ctx context.Context, opts UploadFileOpts) (*provider.FileInfo, error) {
	if opts.Store == nil {
		return nil, ErrStoreRequired
	}
	if len(opts.Data) == 0 {
		return nil, ErrDataRequired
	}
	if opts.Filename == "" {
		return nil, ErrFilenameRequired
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxUploadBytes
	}
	if int64(len(opts.Data)) > maxBytes {
		return nil, ErrDataTooLarge
	}

	if len(opts.MediaTypeAllowlist) > 0 {
		st := sanitizeMediaType(opts.MediaType)
		if st == "" {
			return nil, fmt.Errorf("ai: parse media type: %w", ErrMediaTypeForbidden)
		}
		for _, allow := range opts.MediaTypeAllowlist {
			if st == sanitizeMediaType(allow) {
				goto ok
			}
		}
		return nil, ErrMediaTypeForbidden
	}
ok:

	call := provider.FileUploadCall{
		Data:            opts.Data,
		Filename:        opts.Filename,
		MediaType:       opts.MediaType,
		Purpose:         opts.Purpose,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	return mediaCall(ctx, opts.MaxRetries, call, nil, opts.Store.UploadFile, nil)
}

// DeleteFileOpts options for the DeleteFile function.
type DeleteFileOpts struct {
	Store      provider.FileStore // required
	ID         string             // required
	MaxRetries *int
}

// DeleteFile deletes a previously-uploaded file from the given
// provider.FileStore. It wraps the call in retry logic (default
// maxRetries = 2).
func DeleteFile(ctx context.Context, opts DeleteFileOpts) error {
	if opts.Store == nil {
		return ErrStoreRequired
	}
	if opts.ID == "" {
		return ErrIDRequired
	}

	_, err := mediaCall(ctx, opts.MaxRetries, opts.ID, nil, func(ctx context.Context, id string) (struct{}, error) {
		return struct{}{}, opts.Store.DeleteFile(ctx, id)
	}, nil)
	return err
}
