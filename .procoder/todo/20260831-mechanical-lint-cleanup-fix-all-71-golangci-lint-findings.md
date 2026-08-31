# Mechanical lint cleanup: fix all 71 golangci-lint findings — 50 errcheck (unchecked error returns, concentrated in internal/openaicompat/compattest and internal/geminicompat/compattest SSE fixtures plus test Close() calls), 14 staticcheck (S1016 struct-literal-to-conversion in mcp/, QF1008 selector cleanup in ai/embed_provideroptions_test.go, internal/openaicompat/translation_test.go, internal/websocket/websocket_test.go, mcp/http_test.go QF1006), 3 unused (internal/schema/schema_test.go: field hidden, type embedCycleB), 3 govet inline (internal/schema/schema.go reflect.Ptr), 1 ineffassign (providers/anthropic/language_model.go:341 dead sawMessageStop flag — delete flag and the if branch, behavior unchanged). Verify: golangci-lint run ./... zero findings, procoder test green, procoder check clean.

Status: open
Created: 2026-08-31

## Description

`golangci-lint run ./...` reports 71 findings across the tree, all mechanical.
The repository's [lint] policy is "block", and the audit that established it
found zero findings at the time — this task closes the gap and restores that
invariant. No behavior changes: every fix is a mechanical refactor (error
return handling, struct-literal-to-conversion, selector simplification,
dead-code removal). Findings by linter: 50 errcheck, 14 staticcheck
(S1016 × 9 in mcp/, QF1008 × 4, QF1006 × 1), 3 unused (internal/schema/
schema_test.go), 3 govet inline (internal/schema/schema.go reflect.Ptr),
1 ineffassign (dead `sawMessageStop` in providers/anthropic/language_model.go).

## Acceptance criteria

- [ ] `golangci-lint run ./...` reports zero findings
- [ ] `go vet ./...` reports zero findings
- [ ] `procoder test` passes (all 62 packages)
- [ ] `procoder check` is clean over this change
- [ ] No public API or runtime behavior changed (the ineffassign fix in
      providers/anthropic/language_model.go deletes only the dead flag and
      its always-false branch; the stream-outcome logic keys off
      `haveStopReason` as before)

## Evidence

<!-- Filled at close time: the commands run and what their output proved,
     one line per criterion. Empty evidence keeps the task open. -->
