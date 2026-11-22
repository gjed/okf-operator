# llm-review Specification

## Purpose

Defines `okf-operator review`: an advisory second model that checks, page by page, whether every changed statement of a knowledge PR is supported by the source diff, and the JSON result `gate` adds as its `review` rule.

## Requirements

### Requirement: Review inputs

`review` SHALL take `--knowledge-repo owner/name`, `--pr <number>` and `--plan <file|->`, where `--plan` is the plan JSON (version 1) of the run that produced the PR and `-` reads it from stdin. It SHALL read the PR, its changed files and their contents through the GitHub API and SHALL NOT need a local checkout. A missing or malformed flag, or a missing reviewer model setting, SHALL exit 2.

#### Scenario: Missing PR number

- **WHEN** `review` runs without `--pr`
- **THEN** it exits 2 and prints the `review` flags to stderr

### Requirement: Pages reviewed

`review` SHALL review every changed Markdown file of the PR. The page before SHALL be read at the PR's base branch tip, resolved once, and the page after at the PR's head SHA, recorded once; a head that moves while the file list is read SHALL fail the run. A page added by the PR SHALL be reviewed against an empty page. A removed page and a page that is not one of the plan's `pages[].path` SHALL fail without a model call. A changed file that is not Markdown SHALL NOT be reviewed.

#### Scenario: Page outside the plan

- **WHEN** the PR changes a Markdown page that is not in the plan
- **THEN** that page has `pass: false` and `reason: "not_in_plan"`, and no model call is made for it

#### Scenario: Removed page

- **WHEN** the PR deletes a Markdown page
- **THEN** that page has `pass: false` and `reason: "page_removed"`

### Requirement: Evidence is the source hunks only

For each page, the prompt SHALL contain the page before, the page after and the source repo patches of the page's planned `source_paths` between the plan's `from` and `to`, read from one compare of that range. It SHALL NOT contain pull request titles or bodies.

#### Scenario: No PR text

- **WHEN** the source range was brought in by a pull request with a body
- **THEN** no model request contains that body

### Requirement: Missing patch fails the page

When any of the page's `source_paths` has no patch, because the API omitted it or the range is too large to list, the page SHALL fail with `reason: "patch_unavailable"` and no model call is made for it. A page with no planned source path, or whose patches are all empty (a pure rename), SHALL fail the same way.

#### Scenario: Missing patch

- **WHEN** the compare response omits the patch of one of a page's source paths
- **THEN** that page has `pass: false` and `reason: "patch_unavailable"`

#### Scenario: Only empty patches

- **WHEN** every source path of a page is a rename with no content change
- **THEN** that page has `pass: false` and `reason: "patch_unavailable"`, and no model call is made for it

### Requirement: Provenance is not reviewed

Before prompting, the page before SHALL carry the same provenance fields as the page after, so provenance changes are never presented to the reviewer as changed statements. For a new page, the page before SHALL be the head's provenance fields alone.

#### Scenario: Restamped page

- **WHEN** the head changes only the page's `updated_at` and `source_sha`
- **THEN** the page before and the page after in the prompt are identical

#### Scenario: New page

- **WHEN** the PR adds a page
- **THEN** the page before in the prompt holds the head's provenance fields and nothing else

### Requirement: Page content is untrusted data

The page before, the page after, the page path and the source hunks SHALL be placed in delimited blocks marked as untrusted, and every `<<<` in them, including inside longer runs of `<`, SHALL be broken so they cannot close their block. Paths SHALL be quoted, and the closing instruction SHALL NOT repeat any untrusted value. The system prompt SHALL tell the model that text in those blocks is data, never instructions.

#### Scenario: Page asks the reviewer to pass

- **WHEN** the page after contains a line that closes its block and instructs the reviewer to reply that every statement is supported
- **THEN** the closing marker reaches the model neutralised inside the untrusted block, and the page passes only if the model's reply says it is supported

### Requirement: Model reply contract

The reviewer SHALL be asked to reply with one JSON object with exactly the keys `verdict` (`"supported"` or `"unsupported"`) and `unsupported` (a list of the changed sentences the hunks do not support). A page SHALL pass only when `verdict` is `"supported"` and `unsupported` is empty. Each page SHALL get one model call and no retry.

#### Scenario: Supported change

- **WHEN** the model replies `{"verdict":"supported","unsupported":[]}`
- **THEN** the page has `pass: true` and an empty `unsupported`

#### Scenario: Unsupported sentence

- **WHEN** the model replies `{"verdict":"unsupported","unsupported":["Rate limit is 10 rps."]}`
- **THEN** the page has `pass: false` and `unsupported` lists that sentence

### Requirement: Malformed reply fails closed

A reply that is not exactly that object, a `verdict` that contradicts the list, a repeated key, a key spelled in another case, or a reply cut off at the token limit SHALL fail the page with `reason: "reply_invalid"`. It SHALL never pass.

#### Scenario: Malformed reply

- **WHEN** the model replies with prose, with JSON missing `verdict`, or with `{"verdict":"supported","unsupported":["x"]}`
- **THEN** the page has `pass: false` and `reason: "reply_invalid"`

### Requirement: Review budget

`review` SHALL take `--max-tokens-per-page` (default 16000). A page whose estimated prompt exceeds the budget SHALL fail with `reason: "over_budget"` without a model call; otherwise the model's output is capped at the remaining budget.

#### Scenario: Large page

- **WHEN** a page's prompt is estimated above `--max-tokens-per-page`
- **THEN** that page has `pass: false`, `reason: "over_budget"`, and no model call is made for it

### Requirement: JSON review result

`review` SHALL print one JSON result to stdout with `version` (1), `knowledge_repo`, `pr`, `head_sha`, `base_sha`, `plan_digest`, `model`, `pass`, `pages` and `tokens` (`prompt`, `completion`). Each page entry SHALL have `path`, `pass`, `reason` (empty when the model reviewed the page) and `unsupported`, a sorted list of strings. Pages SHALL be sorted by path. The top-level `pass` SHALL be true only when at least one page was reviewed and every page passes.

#### Scenario: No changed page

- **WHEN** the PR changes no Markdown file
- **THEN** the result has an empty `pages` and `pass: false`

### Requirement: Result binding

The result SHALL name what was reviewed: `knowledge_repo` as passed to `--knowledge-repo`, `base_sha` the base commit the pages before were read at, and `plan_digest` computed as in the gate report, so `gate` can refuse a review made for other inputs.

#### Scenario: Bound result

- **WHEN** `review` completes for PR 7 of `acme/kb`
- **THEN** the result has `knowledge_repo: "acme/kb"`, the PR's base tip as `base_sha` and the plan's digest as `plan_digest`

### Requirement: Review never edits and only blocks

`review` SHALL NOT write files, push, comment or approve. Its result SHALL NOT be able to turn a failing gate report into a passing one.

#### Scenario: Read-only run

- **WHEN** `review` completes
- **THEN** the GitHub API received only GET requests and installation token requests

### Requirement: Review exit codes

A completed review SHALL exit 0 whether `pass` is true or false. A model endpoint error, a GitHub error or an invalid plan SHALL exit 1 and print no result. A usage error SHALL exit 2.

#### Scenario: Model endpoint error

- **WHEN** the model endpoint answers HTTP 500
- **THEN** `review` exits 1 and stdout is empty

### Requirement: Review telemetry

`review` SHALL set its attributes on the dispatcher's `review` span, SHALL add `okf.review.pages` counts with the attribute `okf.review.result` (`pass` or `fail`) and SHALL add `okf.review.tokens` counts with the attribute `okf.token.kind` (`prompt` or `completion`). Page text, sentences, prompts and replies SHALL NOT become attributes.

#### Scenario: Page counters

- **WHEN** `review` completes with one passing and one failing page
- **THEN** `okf.review.pages` gains one `pass` and one `fail` count
