# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues. Use the `gh` CLI for all operations.

## Conventions

Infer the repo from `git remote -v`; `gh` does this automatically inside a clone.

- Create: `gh issue create --title "..." --body "..."`. For a multiline body, use `--body-file <path>`.
- Read: `gh issue view <number> --comments`. Include labels when gathering issue context.
- List: `gh issue list --state open --json number,title,body,labels,comments`, with appropriate label and state filters.
- Comment: `gh issue comment <number> --body "..."`
- Apply labels: `gh issue edit <number> --add-label "..."`
- Remove labels: `gh issue edit <number> --remove-label "..."`
- Close: `gh issue close <number> --comment "..."`

When a skill says "publish to the issue tracker", create a GitHub issue.
When it says "fetch the relevant ticket", read the issue and its comments.

## Pull requests as a triage surface

**PRs as a request surface: no.**

If enabled later, use `gh pr` equivalents for reading, commenting, labelling,
and closing. Read the diff with `gh pr diff <number>`. Include external authors
with association `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR`, or `NONE`; exclude
`OWNER`, `MEMBER`, and `COLLABORATOR`.

GitHub shares one number space across issues and PRs. For an ambiguous reference,
try `gh pr view <number>` and fall back to `gh issue view <number>`.

## Wayfinding operations

Used by `/wayfinder`:

- Map: one issue labelled `wayfinder:map`, containing Notes, Decisions-so-far, and Fog.
- Child ticket: link it to the map as a GitHub sub-issue using `gh api`.
  Where sub-issues are unavailable, add it to a task list in the map body
  and put `Part of #<map>` at the top of the child body.
  Label it `wayfinder:<type>`: `research`, `prototype`, `grilling`, or `task`.
- Blocking: use native GitHub issue dependencies.
  Add an edge with
  `gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`.
  Obtain the database ID with `gh api repos/<owner>/<repo>/issues/<n> --jq .id`;
  it is neither the issue number nor the node ID.
  Where dependencies are unavailable, use a `Blocked by: #<n>, #<n>` line
  at the top of the child body. All blockers must be closed before work starts.
- Frontier: list the map's open children, excluding assigned tickets and those
  with open blockers (`issue_dependencies_summary.blocked_by > 0`, or open
  issues in the fallback line). First in map order wins.
- Claim: `gh issue edit <n> --add-assignee @me`, the session's first write.
- Resolve: comment with the answer, close the ticket, then append a gist and
  link to the map's Decisions-so-far.
