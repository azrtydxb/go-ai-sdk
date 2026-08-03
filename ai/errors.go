package ai

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
)

// APICallError represents an error from an AI provider API call.
type APICallError struct {
	StatusCode   int
	URL          string
	ResponseBody string
	Retryable    bool
	Message      string
}

// Error implements the error interface.
func (e *APICallError) Error() string {
	return fmt.Sprintf("ai: API call to %s failed: status %d: %s", e.URL, e.StatusCode, e.Message)
}

// IsRetryable implements the retry.Retryable interface.
func (e *APICallError) IsRetryable() bool {
	return e.Retryable
}

// NewAPICallError creates a new APICallError with Retryable set based on status code.
// Retryable is true for status codes: 429, 408, or >= 500.
func NewAPICallError(statusCode int, url, body, message string) *APICallError {
	retryable := statusCode == 429 || statusCode == 408 || statusCode >= 500
	return &APICallError{
		StatusCode:   statusCode,
		URL:          url,
		ResponseBody: body,
		Retryable:    retryable,
		Message:      message,
	}
}

// NoObjectGeneratedError is returned when an LLM fails to generate a valid object.
type NoObjectGeneratedError struct {
	RawText string
	Cause   error
}

// Error implements the error interface.
func (e *NoObjectGeneratedError) Error() string {
	return fmt.Sprintf("no object generated: %v", e.Cause)
}

// Unwrap implements the error unwrapping interface.
func (e *NoObjectGeneratedError) Unwrap() error {
	return e.Cause
}

// NoSuchToolError is returned when a tool is not found.
type NoSuchToolError struct {
	ToolName string
}

// Error implements the error interface.
func (e *NoSuchToolError) Error() string {
	return fmt.Sprintf("no such tool: %s", e.ToolName)
}

// InvalidToolArgumentsError is returned when tool arguments are invalid.
type InvalidToolArgumentsError struct {
	ToolName string
	Args     json.RawMessage
	Cause    error
}

// Error implements the error interface.
func (e *InvalidToolArgumentsError) Error() string {
	return fmt.Sprintf("invalid arguments for tool %s: %v", e.ToolName, e.Cause)
}

// Unwrap implements the error unwrapping interface.
func (e *InvalidToolArgumentsError) Unwrap() error {
	return e.Cause
}

// ToolExecutionError is returned when tool execution fails.
type ToolExecutionError struct {
	ToolName string
	Cause    error
}

// Error implements the error interface.
func (e *ToolExecutionError) Error() string {
	return fmt.Sprintf("tool execution error in %s: %v", e.ToolName, e.Cause)
}

// Unwrap implements the error unwrapping interface.
func (e *ToolExecutionError) Unwrap() error {
	return e.Cause
}

// ToolApprovalDeniedError is recorded on a ToolResultRecord.Err (never
// returned/raised directly) when a tool call needing approval was denied —
// see GenerateTextOpts.ApproveToolCall and Approvals.
type ToolApprovalDeniedError struct {
	ToolName string
	Reason   string
}

// Error implements the error interface. Reason is omitted from the message
// when empty.
func (e *ToolApprovalDeniedError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("ai: tool %q execution denied", e.ToolName)
	}
	return fmt.Sprintf("ai: tool %q execution denied: %s", e.ToolName, e.Reason)
}

// RetryError is returned when retries are exhausted.
type RetryError struct {
	Attempts int
	LastErr  error
}

// Error implements the error interface.
func (e *RetryError) Error() string {
	return fmt.Sprintf("retries exhausted after %d attempts: %v", e.Attempts, e.LastErr)
}

// Unwrap implements the error unwrapping interface.
func (e *RetryError) Unwrap() error {
	return e.LastErr
}

// translateRetryErr translates err as returned by retry.Do into the same
// error a caller-facing function (Rerank, Embed, EmbedMany, GenerateText,
// StreamText) ultimately returns: a *retry.ExhaustedError becomes a
// *RetryError; any other error (including nil) passes through unchanged.
//
// This is the single place that implements the lifecycle-callback
// convention: an End callback (OnRerankEnd, OnEmbedEnd, OnModelCallEnd) must
// see the SAME error value the caller gets, never the raw retry-internal
// *retry.ExhaustedError.
func translateRetryErr(err error) error {
	if err == nil {
		return nil
	}
	var exhausted *retry.ExhaustedError
	if errors.As(err, &exhausted) {
		return &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
	}
	return err
}
