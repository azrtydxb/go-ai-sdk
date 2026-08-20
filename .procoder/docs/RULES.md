# Documentation rules

Repo-level documentation rules the procoder harness reads and follows. Edit
freely — what is written here wins over the built-in defaults. The three list
sections below are machine-read (one `- item` per line); everything else is
guidance for the agent.

## Required docs

- README.md
- CHANGELOG.md

## Required badges

- ci
- license

## README first screen

- usp
- badges
- quick start

## Version-tracked docs

- README.md
- CHANGELOG.md

## README must mention

<!-- Optional, blocking when filled: the feature families the README's
     narrative must carry. Matching is case-insensitive and whole-word
     (multi-word phrases allowed); badge images and link targets are
     stripped first, so only prose counts. List what your product IS —
     a family the front page stops telling blocks the gate, which is
     how README rot gets caught at commit time. -->

## Guidance

The README's first screen must sell the project: lead with the one-line value
proposition, then badges, then a quick start a stranger can paste. Diagrams
are Mermaid (they render on GitHub and in the docs site) with the shared
theme in .procoder/docs/mermaid.json. Broken relative links and diagrams
that do not compile are blocking; external links are verified by
`procoder docs --external` and CI — never skipped, never in the write hook.
Keep CHANGELOG.md current: every release gets an entry a user can read.

Standing judgment on `procoder docs` surface-coverage findings ("documentation
never mentions exported X"): they are informational and stay that way. Test
helpers (`ai/aitest`, `internal/**/…test`), everything under `internal/`, and
methods that exist only to satisfy an interface on an unexported type are not
surface a reader is meant to discover, so no markdown page will mention them.
Only exported symbols on `ai`, `agent`, `provider`, and `mcp` that a user
calls directly are worth documenting; treat a finding outside that set as
already answered.

Go-specific: exported symbols carry doc comments (`golint`-style, starting
with the symbol name); inline Go generic syntax — a bracketed type
parameter immediately followed by a parenthesized argument list — parses
as a markdown link; put such signatures in fenced code blocks instead.
