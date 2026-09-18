# Domain Docs

## Layout: single-context

- `CONTEXT.md` at the repo root: domain glossary and context.
- `docs/adr/`: architectural decision records.

## Before exploring

Read `CONTEXT.md` and ADRs relevant to the area being explored.

If these documents do not exist, proceed silently. Domain modeling creates
them lazily when terminology or decisions are resolved; their absence alone
does not require creating them.

## Use the glossary's vocabulary

Use terms defined in `CONTEXT.md` when naming domain concepts in issue titles,
proposals, hypotheses, tests, and other output.

If a concept is missing, reconsider whether it belongs to the domain or note
the gap for `/domain-modeling`.

## Flag ADR conflicts

Explicitly identify any existing ADR your proposal contradicts and explain
why reopening that decision is warranted.
