## mcp/stdio.go exec.Command: suppress the semgrep finding or add a validator?

- **A) Add a lint suppression** with a written justification next to `NewStdioTransport` (mcp/stdio.go:273). The API's purpose is to run developer-configured commands; the doc comment already says callers passing user-influenced input own validation. (Default.)
- **B) Add a defense-in-depth validator** (e.g. reject args containing shell metacharacters or non-absolute first element) that changes runtime behavior.
- **C) Leave as-is** and accept the blocking security finding on the gate.

**Decided: A)** Lint suppression with written justification, added at mcp/stdio.go (nosemgrep comment on the exec.Command call).

## ws scheme: suppress the detect-insecure-websocket ERRORs or leave them blocking?

`internal/websocket.Dial` deliberately supports both the ws (insecure, localhost and test fixtures) and wss (TLS) schemes — the semgrep ERROR on the ws case (websocket.go) is a false positive on a by-design feature; the same rule flags the ws-scheme test-fixture mentions in providers and docs. The gate blocks on that line while it is in scope.

- **A) Add a `nosemgrep: detect-insecure-websocket` suppression** with a justification comment at each flagged line (code sites now done: websocket.go, wsstream.go, deepgram live, openai realtime ×2). (Default — the FP verdict was already in the analysis; this makes it durable.)
- **B) Leave it blocking.** Accept that the gate stays red on this line; every future change touching websocket.go inherits the block.

## Working on the default branch: branch the current change or stay on main?

`[git] default_branch_policy = "block"` and the gate blocks "working directly on the default branch (main)" — this session's changes (websocket.go, mcp/stdio.go, todo, ask files) are uncommitted on main.

- **A) Cut a branch for this change** (e.g. `fix/websocket-tls-and-security-findings`), move the working tree onto it, and commit there. (Default.)
- **B) Keep working on main** and relax the policy (`default_branch_policy = "report"`) in `.procoder/config.toml`.
- **C) Hold the changes uncommitted** until you say how to land them.

## TLS MinVersion in internal/websocket Dial: set explicitly or leave the Go default?

- **A) Set `MinVersion: tls.VersionTLS12` explicitly** in `Dial`'s default TLS config (websocket.go:217). Matches the current Go 1.22+ default, makes it intentional, blocks 1.0/1.1. (Default.)
- **B) Set `MinVersion: tls.VersionTLS13`.** Stricter, may break older endpoints and proxies.
- **C) Leave as-is**, letting Go's default stand; callers can still override via `DialOptions.TLSConfig`.

**Decided: A)** `MinVersion: tls.VersionTLS12` set explicitly in `Dial`'s default TLS config (internal/websocket/websocket.go).

## How to handle the 71 golangci-lint findings?

- **A) One `procoder todo add` for a mechanical cleanup task** fixing errcheck (50), staticcheck (14), unused (3), govet (3), ineffassign (1), then gate + suite. (Default.)
- **B) Fix now in this session**, no todo record.
- **C) Defer** — leave the findings open, revisit later.

**Decided: A)** `procoder todo add` — recorded as `.procoder/todo/20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md`.
