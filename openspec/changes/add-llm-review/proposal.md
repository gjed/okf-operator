# Proposal

## Why

`gate` ([#4](https://github.com/gjed/okf-operator/issues/4)) checks the shape of a knowledge PR but not what its pages say. A generated page can pass every deterministic rule and still state something the source change does not support. An advisory LLM review asks a second model, per changed page, whether every changed statement is backed by the source diff. Its verdict is one more input to the auto-merge decision, and it can only block.

GitHub does not let a PR's author approve it, so the approval that branch protection counts must come from a second, review-only GitHub App.

This is release step 2 of the plan ([#6](https://github.com/gjed/okf-operator/issues/6)).

## What Changes

- New `okf-operator review --knowledge-repo owner/name --pr <n> --plan <file|->` subcommand. For each changed page of the PR it sends the source hunks of the page's planned `source_paths`, the page at the base and the page at the head, and asks whether every changed statement is supported by the hunks. It prints a versioned JSON result: `version`, `pr`, `head_sha`, `model`, `pass`, `pages[]` (`path`, `pass`, `reason`, `unsupported[]`) and `tokens`.
- A missing patch, a page outside the plan, a removed page, a reply that is not the expected JSON, and a page over its token budget all fail the page. Nothing fails open. A model endpoint error fails the run (exit 1, no result).
- New `prompts/review-system.md` and `prompts/review.md`, embedded like the write prompts. Page content and source hunks go in delimited untrusted blocks with the same delimiter neutralisation `write` uses, moved to `prompts.Neutralise` so both commands share it.
- The reviewer model is configured separately from the writer: `--review-model-base-url` / `OKF_REVIEW_MODEL_BASE_URL`, `--review-model` / `OKF_REVIEW_MODEL`, and the key only from `OKF_REVIEW_MODEL_API_KEY`. The writer's `OKF_MODEL_*` settings are never used as a fallback.
- `gate` gains an optional `--review <file>`. It appends a `review` rule after `links`. A review result for a different PR or head SHA fails the run (exit 1).
- New `okf-operator approve --knowledge-repo owner/name --pr <n> --report <file|->` subcommand. It posts an approving review with the review App's credentials only when the gate report passes, contains a passing `review` rule, and the PR head still equals the report's `head_sha`. It prints a small versioned JSON outcome.
- The review App's credentials have their own names (`--review-app-id`, `--review-app-private-key-file`, `OKF_REVIEW_GITHUB_APP_ID`, `OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE`, `OKF_REVIEW_GITHUB_TOKEN`). Only `approve` reads them; `write`, `propose`, `review` and `gate` never do.
- `deploy/review-app-manifest.json` with the minimal permissions (pull requests write, contents read, metadata read), documented in `docs/github-app.md`.
- OTel: attributes on the dispatcher's `review` and `approve` spans, and `okf.review.pages` (by result) and `okf.review.tokens` counters.

Out of scope: merging the PR, posting statuses or comments other than the approval, the controller ([#8](https://github.com/gjed/okf-operator/issues/8)), and changes to `write` behaviour.

## Capabilities

### New Capabilities

- `llm-review`: the `review` subcommand, its inputs, the per-page review contract, the JSON result, its failure modes and telemetry.
- `review-approval`: the `approve` subcommand, the review-only GitHub App identity and its manifest.

### Modified Capabilities

- `auto-merge-gate`: `--review` input, the `review` rule after `links` in the report order, and the head SHA match.
- `model-client`: the reviewer model's separate configuration and the reviewer key's redaction.
- `github-access`: the permissions `review` and `approve` request in App mode, and the separation of the review App's credentials.

## Impact

- Depends on [#15](https://github.com/gjed/okf-operator/pull/15) (model client, write) and [#19](https://github.com/gjed/okf-operator/pull/19) (gate), both merged.
- New packages `internal/review` and `internal/approve`; new `internal/cli/review.go` and `internal/cli/approve.go`, two lines in `internal/cli/commands.go`.
- `internal/gate` gains a `review` rule and a report decoder; `internal/cli/gate.go` gains `--review`.
- `internal/github` gains named credential sources so `approve` loads the review App without touching the default variables. Existing behaviour is unchanged.
- `internal/github/githubtest` gains the pull request reviews endpoint.
- `prompts/` gains a shared `Neutralise`; `internal/write/prompt.go` calls it instead of its own copy. No behaviour change in `write`.
- `README.md` and `docs/github-app.md` overlap with lane C1 ([#16](https://github.com/gjed/okf-operator/issues/16)) and are resolved by rebase.
- No new module dependencies.
