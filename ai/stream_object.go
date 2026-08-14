package ai

import (
	"context"
	"errors"
	"iter"

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
	closed  bool
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
// snapshot. A leading markdown code fence is tolerated (stripped before the
// repair), so models that wrap their JSON in fences still yield snapshots.
// Iteration is single-use: calling Partials() again after
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

		var tracker partialTracker
		// decode is the tracker's mode-specific decoder: unmarshal the prefix
		// the tracker has already fence-stripped and repaired into a T. Only
		// the parts that change the accumulation drive the tracker below — a
		// part that adds nothing to it cannot produce a new snapshot.
		decode := func(repaired string) (any, bool) {
			snap, ok := unmarshalRepairedAs[T](repaired)
			if !ok {
				return nil, false
			}
			return snap, true
		}
		abandoned := false

		for p := range stream.Parts() {
			var v any
			var ok bool
			switch part := p.(type) {
			case provider.TextDelta:
				if !s.toolMode {
					v, ok = tracker.feed([]byte(part.Text), decode)
				}
			case provider.ToolCallDelta:
				if s.toolMode {
					v, ok = tracker.feed([]byte(part.ArgsDelta), decode)
				}
			case provider.ToolCallEnd:
				if s.toolMode {
					// The assembled call carries the authoritative args, so it
					// supersedes the deltas rather than extending them.
					v, ok = tracker.replace(part.Call.Args, decode)
				}
			case provider.FinishPart:
				s.usage = part.Usage
			}

			if ok {
				if !yield(v.(T)) {
					abandoned = true
					break
				}
			}
		}

		s.rawText = tracker.text()
		s.stream = nil

		if abandoned {
			_ = stream.Close()
			s.finalErr = &NoObjectGeneratedError{
				RawText: s.rawText,
				Cause:   errors.New("ai: stream not fully consumed"),
			}
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
// iterated to completion. Returns a *NoObjectGeneratedError if: the
// accumulated text never decoded successfully; Partials() was abandoned
// (the caller stopped ranging over it) before the stream finished; or
// Partials() was never called at all. Final never silently reports a
// zero-value T as success in any of those cases.
func (s *ObjectStream[T]) Final() (T, error) {
	if !s.started {
		var zero T
		return zero, &NoObjectGeneratedError{Cause: errors.New("ai: stream not consumed")}
	}
	return s.final, s.finalErr
}

// Usage returns the usage reported by the stream's FinishPart.
func (s *ObjectStream[T]) Usage() provider.Usage { return s.usage }

// Close releases the underlying provider stream, if one is still open. It
// is idempotent and safe to call at any point: before Partials() has ever
// been ranged over (the caller decided not to consume the stream, so the
// HTTP body would otherwise leak), after Partials() has been fully iterated
// or abandoned (Partials() already closes the stream itself in both cases,
// so Close() is then a no-op), or mid-iteration. Close is not safe for
// concurrent use with Parts().
func (s *ObjectStream[T]) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.stream == nil {
		return nil
	}
	err := s.stream.Close()
	s.stream = nil
	return err
}
