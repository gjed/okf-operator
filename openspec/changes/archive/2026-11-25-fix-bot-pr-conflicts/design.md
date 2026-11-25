# Design

## Context

See proposal.md - Why. A run checks out the open bot PR's head, plans from the cursor, writes, and `propose` adds one commit that fast-forwards the bot branch ([`internal/controller/runner.go`](../../../internal/controller/runner.go), [`internal/propose/propose.go`](../../../internal/propose/propose.go)). Nothing in that path looks at the default branch again, so a PR that has diverged from it stays diverged. `propose` runs in a distroless image with no git binary, so every step below uses the REST git data and pulls APIs.

## Goals / Non-Goals

**Goals:** a bot PR that is behind or in conflict is repaired by the next run, keeping its number and review history; a conflicted PR is never approved; the cursor never moves backwards.

**Non-Goals:** resolving conflicts inside a page at hunk level, repairing a PR a human has pushed non-page files to, making reconcile PRs gateable, closing and re-opening PRs.

## Decisions

### Read the merge state in the controller, not in `propose.Run`

`propose.Run` insists that its knowledge checkout is the PR head, so the head has to be settled before the checkout is made. The controller therefore reads the state in its checkout stage, repairs, and only then checks out and plans. `propose.Run` is unchanged; the repair is a separate, exported step of the `propose` package that the controller calls. The CLI `propose` does not repair: its input contract (checkout equals the PR head) is explicit, and it stays a pure consumer of a plan and a report.

### Poll `unknown`, then carry on

`GET /repos/{owner}/{repo}/pulls/{n}` returns `mergeable_state: "unknown"` while GitHub computes it, which is right after any push. The read retries up to five times with a doubling delay from 500 ms, about 7.5 s in all, and returns `unknown` if it never resolves; the run then carries on as before. A longer wait would hold the per-knowledge-repo run lock for a state that the next run can read. `clean`, `blocked`, `unstable`, `has_hooks` and `draft` also carry on: only `behind` and `dirty` call for a repair. The same bounded read serves `gate`.

### Update-branch with `expected_head_sha`

`behind` calls `PUT .../update-branch` with the head the run read. GitHub answers 202 and moves the branch asynchronously, so the run polls the PR head until it differs from the old one (same bounded backoff) and checks out that. A 422 means the head moved or the base now conflicts; the run fails before any write and the next run reads the state again. The update is a merge of the default branch into the bot branch made by GitHub: no model call, no tree built by the operator. Rejected: building the merge commit ourselves for the `behind` case. It would work, but it duplicates what GitHub already does safely, and `expected_head_sha` gives the same compare-and-swap.

### Regenerate on `dirty`, do not supersede (decided by the user)

The alternative was to close the PR as superseded, record it as declined and leave its pages to the weekly `reconcile`. That is less code, but a conflict then means stale pages for up to a week and a human review of the reconcile PR. Regeneration keeps the PR, its number and its review history, and costs one model call per conflicting page.

### Conflict detection from three trees

For the default head D, the PR head H and their merge base B (from the compare API's `merge_base_commit`), `Tree` lists each commit's blobs. A path is *changed by the bot* when its entry (mode and blob SHA, or absence) differs between B and H, and *changed by the default branch* when it differs between B and D. A path conflicts when both sides changed it and the entries at D and H differ. Both sides making the same change is not a conflict, as in git. This is page granularity: two edits to different hunks of one page conflict, because only the model can merge prose. A tree larger than the API's limit (a truncated listing) fails the run, as `propose` already does.

The commit tree is D's tree, plus H's entry (or its deletion) for every path the bot changed that does not conflict and D did not also change, plus the regenerated content of each conflicting page. Non-conflicting default-branch changes are therefore D's, non-conflicting bot changes are H's, and the commit has parents D and H, so the branch fast-forwards and GitHub sees a merge.

### A page a human edited on the default branch is the base

The conflicting page is regenerated from its content at D: `write` runs in a checkout of D, so the human's version is the page the model updates, with the bot's whole range as evidence. The new provenance (`source_sha` at the range end, `updated_at`, `generated_by`) is what records the regeneration; the PR body gets a note listing the regenerated pages. Flagging the page for a human instead was rejected by the user: the PR would stay conflicted.

### The range comes from the PR body markers

The PR body is the only record of the PR's range that survives a restart: the first marker's `FROM` to the last marker's `TO`. The ranges must be contiguous. The repair refuses, with an error, a body whose oldest ranges were omitted for length (the true start is lost), a range that does not chain, and a section written by `reconcile` (its pages are not tied to its range, so a range cannot select them). It also refuses a conflicting path that the range's plan does not list as a page: nothing can regenerate it. A refusal fails the run visibly rather than carrying on and stacking on a PR that cannot merge. Rejected: reading the commit messages for the full range. They carry abbreviated SHAs only.

### Regenerated plan: the range's plan, filtered

The repair builds the plan for `FROM..TO` against D's checkout with the real routing map, then keeps only the conflicting pages for `write`. The full plan is kept: it is the plan behind the new head, which the controller remembers so a later run that pushes nothing can gate the head again. The cursor reader of that plan is a fixed value, never the real cursor, and nothing in the repair writes the cursor.

### The run goes on after a repair

After a repair the run checks out the new head and plans from the cursor as usual, so source commits merged since the PR's last range are picked up and stacked on the repaired branch. The cursor therefore moves only through `propose`, forwards, as today.

### The `mergeable` rule fails on `dirty` only

The rule reads `mergeable_state` once per gate run through the bounded read and fails when it is `dirty`. `unknown` after the retries passes: GitHub gave no answer, branch protection still refuses to merge a conflicted branch, and failing closed would block every PR that GitHub is slow to compute. The rule goes last of the deterministic rules, before `review`, and `approve` requires it, so a report cannot lose it. The review App's approval of a head that later becomes `dirty` is dismissed by the existing stale-approval dismissal when the run gates that head again.

### Telemetry: a new counter

`okf.propose.prs.updated` is the per-run denominator of the dashboard's per-run panels (0 or 1 per run), so it cannot also count repairs. A new counter `okf.propose.prs.repaired` with attribute `okf.propose.repair` (`update_branch` or `regenerate`) counts them, with the source repo as attribute. The dashboard gets a panel. No alert is added: a repair is the operator healing itself, and a repair that fails shows as a run failure, which the existing alert already covers.

## Risks / Trade-offs

- [Regeneration costs model calls on every conflict, and the model can drop a page] → a dropped or unchanged result keeps the default branch's version of the page; the PR body note says which pages were regenerated and with what result.
- [Page-level conflicts regenerate pages whose hunks did not overlap] → one model call each, and the human's text is the base, so nothing is lost.
- [The merge commit and the old approvals] → an approval of the old head no longer applies to the new one; the review App only approves after a gate run on the new head, and the knowledge repo's branch protection must dismiss stale approvals, as `docs/github-app.md` already says.
- [update-branch is asynchronous] → the run polls the head; if it never moves the run fails and the next run reads again.
- [A reconcile PR that conflicts fails every run until a human resolves or closes it] → closing it lets the next run start a fresh branch; the failure names the PR.
