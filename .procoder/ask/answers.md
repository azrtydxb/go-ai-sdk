# What a human decided

Written 2026-08-31 17:52 UTC. procoder reads this
file to avoid asking a question twice; edit an answer here to change what
it believes. Reword the question and it will be asked again.

## [decision] decisions.md

Key: 2a84c4d24883
Question: TLS MinVersion in internal/websocket Dial: set explicitly or leave the Go default?

- **A) Set `MinVersion: tls.VersionTLS12` explicitly** in `Dial`'s default TLS config (websocket.go:217). Matches the current Go 1.22+ default, makes it intentional, blocks 1.0/1.1. (Default.)
- **B) Set `MinVersion: tls.VersionTLS13`.** Stricter, may break older endpoints and proxies.
- **C) Leave as-is**, letting Go's default stand; callers can still override via `DialOptions.TLSConfig`.

**Decided: A)** `MinVersion: tls.VersionTLS12` set explicitly in `Dial`'s default TLS config (internal/websocket/websocket.go).

Answer: A) MinVersion tls.VersionTLS12 set explicitly in Dial's default TLS config (user: 2A)

## [decision] decisions.md

Key: 53c0b28e3ad3
Question: mcp/stdio.go exec.Command: suppress the semgrep finding or add a validator?

- **A) Add a lint suppression** with a written justification next to `NewStdioTransport` (mcp/stdio.go:273). The API's purpose is to run developer-configured commands; the doc comment already says callers passing user-influenced input own validation. (Default.)
- **B) Add a defense-in-depth validator** (e.g. reject args containing shell metacharacters or non-absolute first element) that changes runtime behavior.
- **C) Leave as-is** and accept the blocking security finding on the gate.

**Decided: A)** Lint suppression with written justification, added at mcp/stdio.go (nosemgrep comment on the exec.Command call).

Answer: A) lint suppression with written justification at mcp/stdio.go (user: 1A)

## [decision] decisions.md

Key: d22cf0a50482
Question: How to handle the 71 golangci-lint findings?

- **A) One `procoder todo add` for a mechanical cleanup task** fixing errcheck (50), staticcheck (14), unused (3), govet (3), ineffassign (1), then gate + suite. (Default.)
- **B) Fix now in this session**, no todo record.
- **C) Defer** — leave the findings open, revisit later.

**Decided: A)** `procoder todo add` — recorded as `.procoder/todo/20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md`.

Answer: A) todo record — .procoder/todo/20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md (user: 3A)
