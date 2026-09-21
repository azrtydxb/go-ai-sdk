# Questions procoder cannot answer for you

Written 2026-09-21 07:15 UTC.

Answer each one by writing a line beginning `Answer: ` under it, then
hand the file back with `procoder ask --file .procoder/ask/QA.md`.
Leave the `Key:` lines alone — they are what ties an answer to its question.

## Q1: [decision] decisions.md

Key: 5e6460d93483
Question: Landing the allowlist commit: the gate's per-file gitleaks scan can't see .gitleaks.toml — how to commit?

Verified: procoder's gate runs `gitleaks dir <changed-file>` per file; gitleaks 8.30 hard-codes the DEFAULT config for single-file sources (repo config only applies to directory scans or via the GITLEAKS_CONFIG env). So the allowlist clears whole-tree scans but not the gate's per-file scan, which still blocks committing `.procoder/ask/` (its `Key:` lines). Options:

- **A) Split the commit**: land `.gitleaks.toml` + docs/code changes now (they contain no Key lines, so the gate passes); leave the ask records (QA.md, answers.md) for a later commit. (Default — gets the reviewed config in without weakening the gate.)
- **B) Patch procoder** (the dev repo at ~/Development/procoder): make the per-file scan pass `-c <root>/.gitleaks.toml` when present, rebuild the launcher binary, then commit everything. Root-cause fix; procoder-side change, out of this session's scope.
- **C) Set GITLEAKS_CONFIG** in the environments the hooks run in, commit everything now. Works with the current binary; depends on the hook process inheriting the variable.
- **D) Skip the gate for this commit** (`--no-verify`). Against the contract; last resort.

**Decided: moot (2026-09-21).** The gate now passes over `.procoder/ask/` with 0 blocking findings, so the records commit normally; no split, patch, env var, or `--no-verify` was needed.

Answer: Moot — the gate now passes over .procoder/ask/ with 0 blocking findings; the records commit normally.

## Q2: [decision] decisions.md

Key: 27c418a9b8f3
Question: Working on the default branch: branch the current change or stay on main?

`[git] default_branch_policy = "block"` and the gate blocks "working directly on the default branch (main)" — this session's changes (websocket.go, mcp/stdio.go, todo, ask files) are uncommitted on main.

- **A) Cut a branch for this change** (e.g. `fix/websocket-tls-and-security-findings`), move the working tree onto it, and commit there. (Default.)
- **B) Keep working on main** and relax the policy (`default_branch_policy = "report"`) in `.procoder/config.toml`.
- **C) Hold the changes uncommitted** until you say how to land them.

**Decided: A) (2026-09-21).** Every change goes through a branch and a PR; `default_branch_policy = "block"` stays.

Answer: A) Branch and PR for every change; default_branch_policy stays "block".

## Q3: [decision] decisions.md

Key: 87c6636ec7d9
Question: [decision] Next Reddit target after the r/golang Small Projects comment

The Small Projects thread comment (p76i8q0) is live. Where to post next?

- **A) r/OpenSource — Open Source Friday thread** — safest; made for this pitch; check the thread's day/week first. (Default.)
- **B) r/modelcontextprotocol** — MCP-client angle; smaller, very on-topic audience.
- **C) r/SideProject** — solo-project culture; good title fit; account-age/karma check first.
- **D) r/programming** — biggest reach; needs the engineering-story framing (iter.Seq
  streaming design, compat-test harness, zero-dep policy); highest downvote risk.
  Better once the repo has visible traction.
- **E) Stop here** — let the r/golang post settle, engage its comments, decide later.

Note: r/OpenSource and r/SideProject enforce account-age/karma posting floors —
verify before posting to either.

Answer: E) Stop here (2026-09-21) — no further Reddit posts for now; let the r/golang Small Projects comment settle and revisit later.

Answer: E) Stop here — no further Reddit posts for now; revisit later.

## Q4: [decision] decisions.md

Key: dd1c14e49bd7
Question: ws scheme: suppress the detect-insecure-websocket ERRORs or leave them blocking?

`internal/websocket.Dial` deliberately supports both the ws (insecure, localhost and test fixtures) and wss (TLS) schemes — the semgrep ERROR on the ws case (websocket.go) is a false positive on a by-design feature; the same rule flags the ws-scheme test-fixture mentions in providers and docs. The gate blocks on that line while it is in scope.

- **A) Add a `nosemgrep: detect-insecure-websocket` suppression** with a justification comment at each flagged line (code sites now done: websocket.go, wsstream.go, deepgram live, openai realtime ×2). (Default — the FP verdict was already in the analysis; this makes it durable.)
- **B) Leave it blocking.** Accept that the gate stays red on this line; every future change touching websocket.go inherits the block.

**Decided: A) (2026-09-21).** The `nosemgrep: detect-insecure-websocket` suppressions with justification comments are in the code at all six flagged sites.

Answer: A) nosemgrep suppressions with justification, in the code at all six flagged sites.
