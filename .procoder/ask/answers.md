# What a human decided

Written 2026-09-21 07:15 UTC. procoder reads this
file to avoid asking a question twice; edit an answer here to change what
it believes. Reword the question and it will be asked again.

## [decision] decisions.md

Key: 0c9984c5bd41
Question: [decision] Reddit: which second subreddit for the go-ai-sdk post?

The r/golang post (1w4c73t) is live. Options for the next Reddit post (same
account, same pitch, adjusted per community):

- **A) r/OpenSource** (ideally the weekly "Open Source Friday" thread) —
  lowest risk, built for exactly this; smaller reach than r/golang.
- **B) r/programming** — big reach (~10M) but strict self-promo norm;
  needs the engineering-story framing (iter.Seq streaming design) to
  survive; higher downvote risk.
- **C) r/mcp (Model Context Protocol community)** — the in-tree MCP
  client (stdio + Streamable HTTP) is the hook; smaller, very on-topic.
- **D) Don't crosspost yet** — let the r/golang post mature (~24h),
  reply to its comments, then decide.

Default: A (safest second post; save B for when the repo has visible
traction).
Answer: superseded by r/golang mod action — the standalone post (1w4c73t) was
held for review as a "small project"; the project was instead posted as a
top-level comment in the weekly Small Projects thread
(our comment: r/golang/comments/1w3ndze/_/p76i8q0). The held
standalone post was left in place (delete not reached via UI; the mod
message says it is queued, not removed, and mods handle it).

Answer: superseded by r/golang mod action — the standalone post (1w4c73t) was

## (no longer asked)

Key: 1cf568bac3d3
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this is a procoder-generated question key (deterministic 12-hex sha1 prefix identifying a settled decision in .procoder/ask/), not a secret

## (no longer asked)

Key: 26318a4947d9
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this 12-hex value is a procoder-generated question key (deterministic sha1 prefix identifying a settled question in .procoder/ask/), not a secret; silenced via .gitleaksignore per the repo RULES.md false-positive policy

## [decision] decisions.md

Key: 27c418a9b8f3
Question: Working on the default branch: branch the current change or stay on main?

`[git] default_branch_policy = "block"` and the gate blocks "working directly on the default branch (main)" — this session's changes (websocket.go, mcp/stdio.go, todo, ask files) are uncommitted on main.

- **A) Cut a branch for this change** (e.g. `fix/websocket-tls-and-security-findings`), move the working tree onto it, and commit there. (Default.)
- **B) Keep working on main** and relax the policy (`default_branch_policy = "report"`) in `.procoder/config.toml`.
- **C) Hold the changes uncommitted** until you say how to land them.

**Decided: A) (2026-09-21).** Every change goes through a branch and a PR; `default_branch_policy = "block"` stays.

Answer: A) Branch and PR for every change; default_branch_policy stays "block".

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

## [decision] decisions.md

Key: 5e6460d93483
Question: Landing the allowlist commit: the gate's per-file gitleaks scan can't see .gitleaks.toml — how to commit?

Verified: procoder's gate runs `gitleaks dir <changed-file>` per file; gitleaks 8.30 hard-codes the DEFAULT config for single-file sources (repo config only applies to directory scans or via the GITLEAKS_CONFIG env). So the allowlist clears whole-tree scans but not the gate's per-file scan, which still blocks committing `.procoder/ask/` (its `Key:` lines). Options:

- **A) Split the commit**: land `.gitleaks.toml` + docs/code changes now (they contain no Key lines, so the gate passes); leave the ask records (QA.md, answers.md) for a later commit. (Default — gets the reviewed config in without weakening the gate.)
- **B) Patch procoder** (the dev repo at ~/Development/procoder): make the per-file scan pass `-c <root>/.gitleaks.toml` when present, rebuild the launcher binary, then commit everything. Root-cause fix; procoder-side change, out of this session's scope.
- **C) Set GITLEAKS_CONFIG** in the environments the hooks run in, commit everything now. Works with the current binary; depends on the hook process inheriting the variable.
- **D) Skip the gate for this commit** (`--no-verify`). Against the contract; last resort.

**Decided: moot (2026-09-21).** The gate now passes over `.procoder/ask/` with 0 blocking findings, so the records commit normally; no split, patch, env var, or `--no-verify` was needed.

Answer: Moot — the gate now passes over .procoder/ask/ with 0 blocking findings; the records commit normally.

## (no longer asked)

Key: 63e8a8acbb32
Question: is this a real credential, or a test value that only looks like one?

Answer: superseded — the .gitleaks.toml allowlist now covers .procoder/ask/; gitleaks (default + repo config) reports 0 leaks on the tree

## (no longer asked)

Key: 706aa1f0344c
Question: [decision] Next Reddit target after the r/golang Small Projects comment

Answer: E) Stop here — no further Reddit posts for now; revisit later.

## (no longer asked)

Key: 77673f4da636
Question: gitleaks flags procoder's own question keys in .procoder/ask/ — how to silence?

Answer: A) scoped allowlist .gitleaks.toml ([extend] useDefault + [[allowlists]] for .procoder/ask/), verified with gitleaks 8.30.1 (user: ok = default)

## (no longer asked)

Key: 8197b535e994
Question: Landing the allowlist commit: the gate's per-file gitleaks scan can't see .gitleaks.toml — how to commit?

Answer: A and B: the split-commit (A) was superseded because the gate scans the whole branch diff, not just staged files — B (patch procoder's per-file gitleaks scan to pass -c when a repo .gitleaks.toml exists, committed in the procoder repo) was done first, then all files committed together (user: A and B next)

## [decision] decisions.md

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

Answer: E) Stop here (2026-09-21) — no further Reddit posts for now; let the r/golang Small Projects comment settle and revisit later.

## (no longer asked)

Key: 92535e04da17
Question: gitleaks flags procoder's own question keys in .procoder/ask/ — how to silence?

Answer: A) scoped allowlist .gitleaks.toml ([extend] useDefault + [[allowlists]] for .procoder/ask/), verified with gitleaks 8.30.1 (user: ok = default)

## [decision] decisions.md

Key: a4a3e3631538
Question: Issues #4/#5 (subscription auth): the audit found acceptance criteria unmet — close, or build the gaps?

Audit of HEAD against the issues' acceptance criteria (verified in code): Codex has no automatic token refresh (`providers/codex` documents "the caller owns refresh"; the transport never checks expiry); device-code login exists only in `internal/codexauth` with no public entry point; the 0600/0700 credential writers exist only in `internal/` for both providers, and package `auth` states "without credential persistence. Callers own secure storage". Claude Pro/Max is otherwise met (auto-refresh, bearer + betas, `AuthMode()` diagnostics). Non-goals respected for both.

- **A) Build the gaps**: public device-code login, Codex auto-refresh on the request path (mirroring anthropic's token source), and a public file credential store (0600/0700) for both providers; then close #4 and #5. Reverses the "callers own storage" stance in package `auth`. (Default.)
- **B) Keep the caller-owns-storage design**: close #5 as met, add only Codex auto-refresh + public device-code login, and amend the issues to say persistence is deliberately the caller's.
- **C) Close both as-is** with a comment recording the deliberate deviations.
- **D) Leave both open**, no work now.

**Decided: A)** Build all gaps — public device-code login, Codex auto-refresh, public 0600/0700 file credential store for both providers; then close #4 and #5.

Answer: A) Build the gaps — done in PR #9, released in v0.6.0; #4 and #5 closed.

## (no longer asked)

Key: cba0099cf8f8
Question: Working on the default branch: branch the current change or stay on main?

Answer: A) branch fix/websocket-tls-and-security-findings cut from main, change committed there (user: ok = default)

## (no longer asked)

Key: d22cf0a50482
Question: How to handle the 71 golangci-lint findings?

Answer: A) todo record — .procoder/todo/20260831-mechanical-lint-cleanup-fix-all-71-golangci-lint-findings.md (user: 3A)

## [decision] decisions.md

Key: dd1c14e49bd7
Question: ws scheme: suppress the detect-insecure-websocket ERRORs or leave them blocking?

`internal/websocket.Dial` deliberately supports both the ws (insecure, localhost and test fixtures) and wss (TLS) schemes — the semgrep ERROR on the ws case (websocket.go) is a false positive on a by-design feature; the same rule flags the ws-scheme test-fixture mentions in providers and docs. The gate blocks on that line while it is in scope.

- **A) Add a `nosemgrep: detect-insecure-websocket` suppression** with a justification comment at each flagged line (code sites now done: websocket.go, wsstream.go, deepgram live, openai realtime ×2). (Default — the FP verdict was already in the analysis; this makes it durable.)
- **B) Leave it blocking.** Accept that the gate stays red on this line; every future change touching websocket.go inherits the block.

**Decided: A) (2026-09-21).** The `nosemgrep: detect-insecure-websocket` suppressions with justification comments are in the code at all six flagged sites.

Answer: A) nosemgrep suppressions with justification, in the code at all six flagged sites.

## (no longer asked)

Key: e7ba65500d07
Question: is this a real credential, or a test value that only looks like one?

Answer: not a credential — this 12-hex value is a procoder-generated question key (deterministic sha1 prefix identifying a settled question in .procoder/ask/), not a secret; silenced via .gitleaksignore per the repo RULES.md false-positive policy
