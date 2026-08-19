# Development workflow

This repository is governed by [procoder](https://github.com/procoder)
(a Claude Code plugin): formatting, hygiene, security, and documentation
checks run as commit gates and CI-style sweeps. The repo-level rules the
harness follows live in [`.procoder/`](../.procoder/docs/RULES.md) —
documentation rules, security rules, the PR/commit templates, the workflow
rules, and the pre-PR review rubric.

## Day-to-day commands

- `procoder doctor` — which formatters this repo needs and which are
  installed; `procoder init` prints (or runs) the install commands for
  anything missing.
- `procoder format <files>` — the formatted result for review;
  `procoder check` — the commit gate over changed files.
- `procoder lint` — the canonical linter per ecosystem (golangci-lint
  here); findings are diagnoses to judge, fix, or explain.
- `procoder index build` — the code index; then `procoder index`
  subcommands (find, search, refs, outline, impact, callers, unused)
  instead of grepping blind.
- `procoder git` — the pre-finish status: branch, hygiene, message
  checks, workflow lint, template state.
- `procoder security` — secrets scan (blocking) plus, with `--deep`,
  SAST and dependency vulnerabilities.
- `procoder docs` — broken references, diagrams, drift, badges, README
  structure; `--external` adds link checking.
- `procoder ci` — workflow hygiene: pinned actions, timeouts,
  concurrency, tests exist.
- `procoder infra` — DevOps hygiene where the files exist (none in this
  repo today: no Dockerfiles, Terraform, or Kubernetes manifests).
- `procoder maintain` — dead-code candidates, complexity, function
  length; `procoder debt` — harvests `debt:` markers into a ledger.
- `procoder audit` — every domain's checks over the whole tree (the
  onboarding sweep this repo went through).

## Process commands

- `procoder spec` / `procoder plan` / `procoder todo` — spec-first
  design, implementation planning, and the quality-gated task list.
- `procoder agents` — keeps per-host agent rule files in sync with
  AGENTS.md.
- `procoder templates` — prints the default `.procoder/` files;
  `procoder scrub` — pre-PR content scrub; `procoder lessons` — the
  lessons ledger; `procoder principles` — the engineering principles;
  `procoder hook` — the write-hook entry point; `procoder version` —
  the plugin version.

## Repo conventions

- The root module is **zero-dependency** by policy; external deps live
  only in nested `contrib/*` modules (`contrib/otel`).
- Providers never retry — the `ai` core owns retries.
- Public API is Vercel-AI-SDK-parity surface: additive changes only,
  outside a sanctioned breaking revision.
- Branch per change; merge to `main` when the gate is clean.
