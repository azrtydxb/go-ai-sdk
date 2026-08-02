package ai

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"reflect"

	"github.com/azrtydxb/go-ai-sdk/internal/partialjson"
	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ObjectStream is the result of StreamObject: a single-use iterator over
// snapshots of T decoded from the model's incrementally streamed output,
// plus accumulated results available after iteration completes.
type ObjectStream[T any] struct {
	ctx      context.Context
	stream   provider.StreamResponse
	toolMode bool

	started bool
	err     error

	usage    provider.Usage
	rawText  string
	final    T
	finalErr error
}

// StreamObject starts the model stream (retried like StreamText) and
// returns an *ObjectStream. A non-nil error means the stream could not
// start.
func StreamObject[T any](ctx context.Context, opts GenerateObjectOpts) (*ObjectStream[T], error) {
	call, toolName, err := buildObjectCall[T](opts)
	if err != nil {
		return nil, err
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	stream, err := retry.Do(ctx, maxRetries, func() (provider.StreamResponse, error) {
		return opts.Model.Stream(ctx, call)
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
		return nil, err
	}

	return &ObjectStream[T]{
		ctx:      ctx,
		stream:   stream,
		toolMode: toolName != "",
	}, nil
}

// Partials yields a new T snapshot each time the accumulated JSON — text
// deltas in native JSON mode, or the forced tool call's argument deltas in
// tool mode — repaired via partialjson.Repair, unmarshals successfully into
// T and differs (via reflect.DeepEqual) from the previously yielded
// snapshot. Iteration is single-use: calling Partials() again after
// exhausting (or abandoning) it yields nothing. The underlying provider
// stream is closed when iteration ends, including on early abandonment.
func (s *ObjectStream[T]) Partials() iter.Seq[T] {
	return func(yield func(T) bool) {
		if s.started {
			return
		}
		s.started = true

		stream := s.stream
		if stream == nil {
			return
		}

		var accum []byte
		var have bool
		var prev T
		abandoned := false

		for p := range stream.Parts() {
			switch part := p.(type) {
			case provider.TextDelta:
				if !s.toolMode {
					accum = append(accum, part.Text...)
				}
			case provider.ToolCallDelta:
				if s.toolMode {
					accum = append(accum, part.ArgsDelta...)
				}
			case provider.ToolCallEnd:
				if s.toolMode {
					accum = append(accum[:0], part.Call.Args...)
				}
			case provider.FinishPart:
				s.usage = part.Usage
			}

			if repaired, ok := partialjson.Repair(string(accum)); ok {
				var snap T
				if err := json.Unmarshal([]byte(repaired), &snap); err == nil {
					if !have || !reflect.DeepEqual(snap, prev) {
						have = true
						prev = snap
						if !yield(snap) {
							abandoned = true
							break
						}
					}
				}
			}
		}

		s.rawText = string(accum)
		s.stream = nil

		if abandoned {
			_ = stream.Close()
			return
		}

		if err := stream.Err(); err != nil {
			s.err = err
		}
		_ = stream.Close()

		s.final, s.finalErr = decodeObject[T](s.rawText)
	}
}

// Err returns the error, if any, that ended iteration abnormally: a
// *RetryError from stream start failures are returned by StreamObject
// itself, so Err reflects only the underlying provider stream's mid-stream
// error.
func (s *ObjectStream[T]) Err() error { return s.err }

// Final returns the last valid decode of the complete accumulated stream
// text (fences stripped, not partialjson-repaired — the finished stream is
// expected to be complete JSON). Valid only after Partials() has been
// iterated to completion. Returns a *NoObjectGeneratedError if the
// accumulated text never decoded successfully.
func (s *ObjectStream[T]) Final() (T, error) {
	return s.final, s.finalErr
}

// Usage returns the usage reported by the stream's FinishPart.
func (s *ObjectStream[T]) Usage() provider.Usage { return s.usage }
