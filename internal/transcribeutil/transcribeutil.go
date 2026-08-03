// Package transcribeutil holds small helpers shared by the asynchronous
// (upload-then-poll) transcription providers — assemblyai, gladia, and
// revai — whose Transcribe flows are structurally identical (upload/create a
// job, poll it to a terminal state, ctx-aware between polls) even though
// each speaks a different wire format.
package transcribeutil

import (
	"context"
	"time"
)

// Sleep blocks for d or until ctx is done, whichever comes first, returning
// ctx.Err() in the latter case. Used to wait out a provider's poll interval
// between transcript/job status checks without ignoring cancellation.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ExtForMediaType returns a plausible file extension (including the leading
// dot) for a MIME media type, used to build a filename for a multipart
// upload. Returns "" when the type is unknown or empty.
func ExtForMediaType(mediaType string) string {
	switch mediaType {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	}
	if mediaType == "" {
		return ""
	}
	for i := len(mediaType) - 1; i >= 0; i-- {
		if mediaType[i] == '/' {
			if i+1 < len(mediaType) {
				return "." + mediaType[i+1:]
			}
			return ""
		}
	}
	return ""
}
