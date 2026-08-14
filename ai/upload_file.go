package ai

import (
	"context"
	"errors"

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

// UploadFileOpts options for the UploadFile function.
type UploadFileOpts struct {
	Store           provider.FileStore // required
	Data            []byte             // required
	Filename        string             // required
	MediaType       string
	Purpose         string
	MaxRetries      *int
	ProviderOptions map[string]any

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
