# [todo] mechanical lint cleanup: fix all golangci-lint findings

- created: 2026-08-31
- branch: fix/mechanical-lint-cleanup (PR #3)
- status: done in PR #3, pending merge

## Task

Bring `golangci-lint run ./...` to zero findings. The original todo
was cut from an initial scan that surfaced 71 findings
(50 errcheck, 14 staticcheck, 3 unused, 3 govet inline, 1
ineffassign). The full errcheck surface is larger once every package
builds — the final fix commit touched 142 files (1325 insertions,
1281 deletions) and the linter now reports **0 issues** module-wide.

## Findings and fixes

- **errcheck** (the bulk):
  - `defer X.Close()` in production request/response paths →
    `defer func() { _ = X.Close() }()` (explicit, documented discard).
  - bare `.Close()`/`.Write()`/`.Unmarshal()`/`.Fprintf()` statement
    sites in tests/helpers → `_ =`, `_, _ =`, `_, _, _ =`
    (`websockettest.ReadMessage` returns three values), or a real
    `t.Fatalf` check where the value is consumed.
  - `httptest.Server.Close()` (no return value) stays a plain call.
- **staticcheck**: S1016 struct-literal copies → direct type
  conversions (`mcp/*`, `ai/middleware`); QF1006 redundant embedded
  selectors removed; QF1008 loop condition lifted in `mcp/http_test`.
- **unused**: schema cycle-test fixtures referenced via blank
  identifiers + symmetric assertions; `hidden` field gets `json:"-"`.
- **ineffassign**: dead `sawMessageStop` flag removed
  (`providers/anthropic` — always false after the loop; the
  `message_stop` case returns early).
- **govet inline**: `reflect.Ptr` → `reflect.Pointer` (the `//go:fix`
  directive; `Ptr` is a deprecated alias).

## Verification (all green in the fix commit)

- [x] `golangci-lint run ./...` → 0 issues
- [x] `go test ./...` → 62 packages pass
- [x] `go vet ./...` clean; `gofmt -l` clean
- [x] `procoder check` gate clean at commit time

## Close

- [ ] merge PR #3 into main
- [ ] `procoder todo close 20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md`
