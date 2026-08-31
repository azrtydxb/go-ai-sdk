# What a human decided

Written 2026-08-31 19:28 UTC. procoder reads this
file to avoid asking a question twice; edit an answer here to change what
it believes. Reword the question and it will be asked again.

## (no longer asked)

Key: 1cf568bac3d3
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this is a procoder-generated question key (deterministic 12-hex sha1 prefix identifying a settled decision in .procoder/ask/), not a secret

## (no longer asked)

Key: 26318a4947d9
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this 12-hex value is a procoder-generated question key (deterministic sha1 prefix identifying a settled question in .procoder/ask/), not a secret; silenced via .gitleaksignore per the repo RULES.md false-positive policy

## (no longer asked)

Key: 2a84c4d24883
Question: TLS MinVersion in internal/websocket Dial: set explicitly or leave the Go default?

Answer: A) MinVersion tls.VersionTLS12 set explicitly in Dial's default TLS config (user: 2A)

## (no longer asked)

Key: 53c0b28e3ad3
Question: mcp/stdio.go exec.Command: suppress the semgrep finding or add a validator?

Answer: A) lint suppression with written justification at mcp/stdio.go (user: 1A)

## (no longer asked)

Key: 59314a2bed80
Question: ws scheme: suppress the detect-insecure-websocket ERRORs or leave them blocking?

Answer: A) nosemgrep suppressions added with justification at the flagged lines; verified 0 blocking security findings (user: ok = default)

## (no longer asked)

Key: 63e8a8acbb32
Question: is this a real credential, or a test value that only looks like one?

Answer: superseded — the .gitleaks.toml allowlist now covers .procoder/ask/; gitleaks (default + repo config) reports 0 leaks on the tree

## (no longer asked)

Key: 77673f4da636
Question: gitleaks flags procoder's own question keys in .procoder/ask/ — how to silence?

Answer: A) scoped allowlist .gitleaks.toml ([extend] useDefault + [[allowlists]] for .procoder/ask/), verified with gitleaks 8.30.1 (user: ok = default)

## [decision] decisions.md

Key: 8197b535e994
Question: Landing the allowlist commit: the gate's per-file gitleaks scan can't see .gitleaks.toml — how to commit?

Verified: procoder's gate runs `gitleaks dir <changed-file>` per file; gitleaks 8.30 hard-codes the DEFAULT config for single-file sources (repo config only applies to directory scans or via the GITLEAKS_CONFIG env). So the allowlist clears whole-tree scans but not the gate's per-file scan, which still blocks committing `.procoder/ask/` (its `Key:` lines). Options:

- **A) Split the commit**: land `.gitleaks.toml` + docs/code changes now (they contain no Key lines, so the gate passes); leave the ask records (QA.md, answers.md) for a later commit. (Default — gets the reviewed config in without weakening the gate.)
- **B) Patch procoder** (the dev repo at ~/Development/procoder): make the per-file scan pass `-c <root>/.gitleaks.toml` when present, rebuild the launcher binary, then commit everything. Root-cause fix; procoder-side change, out of this session's scope.
- **C) Set GITLEAKS_CONFIG** in the environments the hooks run in, commit everything now. Works with the current binary; depends on the hook process inheriting the variable.
- **D) Skip the gate for this commit** (`--no-verify`). Against the contract; last resort.

Answer: A and B: the split-commit (A) was superseded because the gate scans the whole branch diff, not just staged files — B (patch procoder's per-file gitleaks scan to pass -c when a repo .gitleaks.toml exists, committed in the procoder repo) was done first, then all files committed together (user: A and B next)

## (no longer asked)

Key: 92535e04da17
Question: gitleaks flags procoder's own question keys in .procoder/ask/ — how to silence?

Answer: A) scoped allowlist .gitleaks.toml ([extend] useDefault + [[allowlists]] for .procoder/ask/), verified with gitleaks 8.30.1 (user: ok = default)

## (no longer asked)

Key: cba0099cf8f8
Question: Working on the default branch: branch the current change or stay on main?

Answer: A) branch fix/websocket-tls-and-security-findings cut from main, change committed there (user: ok = default)

## (no longer asked)

Key: d22cf0a50482
Question: How to handle the 71 golangci-lint findings?

Answer: A) todo record — .procoder/todo/20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md (user: 3A)

## (no longer asked)

Key: e7ba65500d07
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this 12-hex value is a procoder-generated question key (deterministic sha1 prefix identifying a settled question in .procoder/ask/), not a secret; silenced via .gitleaksignore per the repo RULES.md false-positive policy
