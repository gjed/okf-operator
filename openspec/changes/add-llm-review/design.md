# Design

## Context

`write` ([#15](https://github.com/gjed/okf-operator/pull/15)) generates pages with `internal/model.Client`, and `gate` ([#19](https://github.com/gjed/okf-operator/pull/19)) prints a report whose `pass` is the conjunction of its rules. The review adds a second model's opinion per page and hands it to `gate` as one more rule. See the specs for the required behaviour.

## Goals / Non-Goals

**Goals:**

- A review that can only block: every unexpected state fails the page.
- Packages the controller can call directly, with thin CLI wrappers.
- The review App's credentials isolated in one command.

**Non-Goals:**

- Merging, commit statuses, comments other than the approval.
- A retry loop or a second opinion inside `review`.
- Changing `write` behaviour.

## Decisions

### Package layout

```text
internal/review/      # Run(ctx, deps, repo, n, plan) -> Result; prompt; result JSON; telemetry
internal/approve/     # Run(ctx, deps, repo, n, report) -> Outcome
internal/cli/review.go, internal/cli/approve.go
prompts/review-system.md, prompts/review.md, prompts/untrusted.go
deploy/review-app-manifest.json
```

`internal/review` imports `internal/gate` for its `Reader` (PR, files, contents at a ref) and its GitHub implementation, so the PR is read exactly as `gate` reads it. To avoid an import cycle, `gate` does not import `review`: it takes a `gate.Review` value (PR, head SHA, pass, details), and `review.Result.GateInput()` builds it. `internal/cli/gate.go` decodes the file with `review.Decode`.

### Where the approval is posted

A separate `approve` subcommand takes the combined gate report. Rejected: posting inside `review`, because the review alone does not know whether the deterministic rules passed, and the review App would then sit in the same process as the model call; leaving it to the controller ([#8](https://github.com/gjed/okf-operator/issues/8)), because the controller does not exist yet and every stage is meant to run standalone. `approve` also requires a passing `review` rule, so a gate run without `--review` never yields an approval.

### Review App credentials

`internal/github` gains `CredentialNames` and `LoadNamed`. `Load` becomes `LoadNamed` with the default names, so existing commands are unchanged. `approve` passes the review names (`review-app-id`, `review-app-private-key-file`, `OKF_REVIEW_GITHUB_APP_ID`, `OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE`, `OKF_REVIEW_GITHUB_TOKEN`), so it never sees the writer's variables and the writer never sees the review ones. Rejected: reusing `--app-id` in `approve` (two identities under one name), and an env remapping wrapper around `Load` (its errors would name the wrong variables).

### Evidence and prompt

- Evidence is the compare patches of the page's planned `source_paths` for `from...to`, from one `plan.GitHubDiffer.Range` call, exactly as `write` reads hunks. PR titles and bodies are not sent: the question is whether the hunks support the page, and PR text widens the injection surface. Rejected: sending PR text like `write`.
- `plan.ErrIncomplete` (300 or more files in the range) fails every page with `patch_unavailable`. Any other compare error fails the run.
- The page before is read at the base branch tip that `gate.GitHubReader` resolves, the same commit `gate` reads policy at. If the base branch changed the page after the PR was opened, the diff shows that change reverted and the model flags it, which fails closed. Rejected: the PR's `base.sha`, which `gate` does not read and which would need a second PR reader.
- Provenance noise: the page before is stamped with the page after's provenance fields via `provenance.Parse` and `provenance.Stamp`, so `updated_at` and friends never look like changed statements. When either page has no frontmatter, both are sent as they are. Rejected: telling the model to ignore those keys, which is not deterministic.
- `prompts.Neutralise` replaces `write`'s private `neutralise`, which now calls it. The markers are defined by the prompts, so the shared helper lives next to them. Rejected: a copy in `internal/review`.

### Reply contract

One JSON object `{"verdict": "supported"|"unsupported", "unsupported": [...]}`, decoded strictly after removing one surrounding code fence. Anything else, a contradiction between `verdict` and the list, or `finish_reason: length` fails the page with `reply_invalid`. One call per page, no retry: a retry would let a bad reply turn into a pass on the second roll. Sentences are trimmed, empty ones dropped, then sorted and deduplicated so the result and the gate details are stable.

### Budget

`--max-tokens-per-page` (default 16000) with the same conservative estimate as `write` (one token per three bytes). An over-budget page fails without a call. There is no `--max-pages`: the PR came from `write`, which already caps the page count. Rejected: failing the whole run like `write` does, because a fail on one page is enough to block.

### Gate integration

`gate.Deps` gains an optional `Review *Review`. After `links`, `Evaluate` checks its PR and head SHA against the PR it read (mismatch is an error) and appends `review` through `Report.Add`. A failing review with no details still fails, because `Finalize` keeps a false `Pass`. When the review result has no pages, the detail is `no changed page was reviewed`.

### Approve flow

1. Decode the report strictly (`gate.Decode`: version 1, unknown fields rejected) and check `pr`.
1. Recompute `pass` with `Finalize` on a copy, so a hand-edited `pass: true` over a failing rule does not count.
1. Require a passing `review` rule.
1. Re-read the PR head with the review App's token; a moved head declines with `head_moved`.
1. `POST /repos/{kb}/pulls/{n}/reviews` with `event: APPROVE` and `commit_id: head_sha`.

The outcome is JSON with `approved` and `reason`, and exit 0 for a decision either way.

### Telemetry

`review` sets `okf.knowledge.repo`, `okf.source.repo`, `okf.bundle`, `okf.review.pr`, `okf.review.head_sha`, `okf.review.model`, `okf.review.pass`, page counts and token counts on the dispatcher span, and adds `okf.review.pages` by `okf.review.result` and `okf.review.tokens` by `okf.token.kind`, named like `okf.write.*`. `approve` sets four `okf.approve.*` attributes. No page text, sentence, prompt or reply is ever an attribute.

## Risks / Trade-offs

- [The reviewer is the same family of model as the writer and may share its blind spots] → It is advisory and can only block. The deterministic rules stay the main gate.
- \[A lazy reviewer replying `supported` for everything\] → Cannot be prevented by the tool. Configure a reviewer model different from the writer.
- [Base branch moved since the PR was opened] → Fails closed; the controller re-runs on a rebased PR.
- \[A head push between `gate` and `approve`\] → `approve` re-reads the head and posts on the reviewed commit only.
