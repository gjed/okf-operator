# page-write Specification

## Purpose

Defines the `write` subcommand: given a plan, it asks a model for the updated content of each planned page, validates it, stamps provenance and writes the changed pages to a knowledge checkout. Its JSON report and the modified working tree are the input contract for `propose`.

## Requirements

### Requirement: Write inputs

`write` SHALL require `--plan <file|->` (a plan v1 JSON document, `-` for stdin) and `--knowledge-dir <path>` (a checkout of the knowledge repo). It SHALL accept `--max-pages` (default 10) and `--max-tokens-per-page` (default 16000), the model flags from the `model-client` spec and the GitHub App flags from the `github-access` spec. A plan whose `version` is not `1`, or with unknown keys, SHALL be a runtime error.

#### Scenario: Plan from stdin

- **WHEN** the user runs `okf-operator plan ... | okf-operator write --plan - --knowledge-dir kb`
- **THEN** `write` reads the plan from stdin

#### Scenario: Missing knowledge dir

- **WHEN** `write` runs without `--knowledge-dir`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Unsupported plan version

- **WHEN** the plan document has `version: 2`
- **THEN** `write` exits 1 before any GitHub or model call

### Requirement: Routed set enforcement

`write` SHALL write only files that are `pages[].path` entries of the plan. Each page path SHALL be a clean relative `.md` path inside the plan's bundle directory, outside `.kb/`, and SHALL resolve inside `--knowledge-dir` without following a symlink out of it; otherwise the run exits 1 before any model call. Model output is never interpreted as a path: each model call produces at most the content of the one page it was made for.

#### Scenario: Instructions in a source PR body

- **WHEN** a source PR body in the range says to rewrite another page of the bundle
- **THEN** after the run every file under `--knowledge-dir` that is not a planned page path is byte-identical to before

#### Scenario: Page path escapes the bundle

- **WHEN** a plan page path is `../other/page.md`
- **THEN** `write` exits 1 and no model call is made

### Requirement: Diff hunks per page

For each page, `write` SHALL send the model the compare patches of that page's `source_paths` only. When the patch of any of those paths is missing or truncated, the page SHALL be dropped with reason `patch_unavailable` and no model call SHALL be made for it.

#### Scenario: Truncated patch

- **WHEN** the compare API returns no `patch` for one of a page's source paths
- **THEN** the page is reported as dropped with reason `patch_unavailable` and the model receives no request for it

### Requirement: Source PR context is untrusted

`write` SHALL include the titles and bodies of the PRs that introduced the commits in `from..to` in the prompt, inside a delimited block that the prompt declares untrusted data. Occurrences of the delimiter inside PR text or hunks SHALL be neutralised so the block cannot be closed early.

#### Scenario: Delimiter in a PR body

- **WHEN** a PR body contains the closing delimiter of the untrusted block
- **THEN** the prompt sent to the model contains exactly one closing delimiter for that block

### Requirement: Model response contract

The model SHALL reply with either the literal `NO_CHANGE` or the complete updated page (frontmatter and body). A reply wrapped in one Markdown code fence SHALL be unwrapped. A reply that is neither, that ends because of the token limit, that contains a prompt block marker line, or whose frontmatter `type` differs from the current page's, SHALL count as an invalid page.

#### Scenario: Explicit no change

- **WHEN** the model replies `NO_CHANGE` for a page
- **THEN** the page is reported as `unchanged` and its file is not modified

#### Scenario: Page updated

- **WHEN** the model replies with a valid updated page
- **THEN** the file is rewritten with the model's content plus stamped provenance, and the page is reported as `changed`

#### Scenario: Reply echoes the prompt

- **WHEN** the model returns the page followed by a reference page block copied from the prompt
- **THEN** the reply counts as an invalid page

#### Scenario: Reply changes the page type

- **WHEN** the current page has `type: Compute` and the reply has `type: Storage`
- **THEN** the reply counts as an invalid page

#### Scenario: Same content returned

- **WHEN** the model returns the current page unchanged apart from provenance keys
- **THEN** the page is reported as `unchanged` and its file, including provenance, is not modified

### Requirement: Page validation with one retry

Each model page SHALL be validated as an OKF concept document before it is accepted. On failure, `write` SHALL make one more model call that includes the validator error. If the second reply also fails, the page SHALL be dropped with reason `validation_failed`.

#### Scenario: Invalid page retried once then dropped

- **WHEN** the model returns an invalid page twice for the same page
- **THEN** exactly two model calls are made for it, the file is not modified, and the page is reported as dropped with reason `validation_failed`

#### Scenario: Retry succeeds

- **WHEN** the first reply is invalid and the second is valid
- **THEN** the page is reported as `changed` with `attempts: 2`

### Requirement: Hard budget

Before the first model call, `write` SHALL check that the number of `update` pages does not exceed `--max-pages` and that every page's estimated first-attempt prompt plus its current page size fits in `--max-tokens-per-page`. Either failure SHALL exit 1 with no model call. `--max-tokens-per-page` SHALL bound the total tokens of all attempts for one page; a retry that would exceed it SHALL NOT be made and the page SHALL be dropped with reason `validation_failed`.

#### Scenario: Budget exceeded

- **WHEN** the plan has 3 `update` pages and `--max-pages 2` is set
- **THEN** `write` exits 1, the error names the limit, and the model endpoint receives no request

#### Scenario: Page too large for the token budget

- **WHEN** one page's estimated prompt exceeds `--max-tokens-per-page`
- **THEN** `write` exits 1 before any model call and names the page

### Requirement: Page creation is out of scope

Pages with `state: create` or `state: missing` SHALL NOT be sent to the model. They SHALL be reported as dropped with reason `create_unsupported` or `page_missing`, and they SHALL NOT count against `--max-pages`.

#### Scenario: Create page in the plan

- **WHEN** the plan has a page with `state: create`
- **THEN** the report lists it as dropped with reason `create_unsupported` and no file is created

### Requirement: All-or-nothing working tree

`write` SHALL modify files under `--knowledge-dir` only after every planned page has been processed. When the run exits non-zero, no file under `--knowledge-dir` SHALL have been modified.

#### Scenario: Model endpoint fails mid-run

- **WHEN** the model endpoint returns HTTP 500 for the second of three pages
- **THEN** `write` exits 1 and no file under `--knowledge-dir` is modified

### Requirement: JSON write report

On success, `write` SHALL print one JSON document to stdout with `version: 1`, `source`, `bundle`, `from`, `to`, `model`, `generated_by`, `pages` and `tokens` (run totals). Each page entry SHALL carry `path`, `result` (`changed`, `unchanged` or `dropped`), `reason` (empty unless dropped), `attempts` and `tokens` (`prompt`, `completion`). `pages` SHALL list every plan page sorted by path.

#### Scenario: Mixed results

- **WHEN** a run changes one page, leaves one unchanged and drops one
- **THEN** the report lists all three sorted by path with their results and reasons

### Requirement: Propose input contract

The write report and the files modified under `--knowledge-dir` SHALL be the input contract for `propose`: exactly the pages with result `changed` are modified, and no other file is created, modified or deleted.

#### Scenario: Working tree matches the report

- **WHEN** a run reports one page `changed`, one `unchanged` and one `dropped`
- **THEN** `git status --porcelain` in the checkout lists only the changed page

#### Scenario: Empty plan

- **WHEN** the plan has no pages
- **THEN** `write` exits 0, makes no GitHub or model call, and prints a report with an empty `pages` list and zero tokens

### Requirement: Write telemetry

Each `write` run SHALL annotate the dispatcher's `write` span with the source repo, bundle, model and the counts of changed, unchanged and dropped pages, and SHALL record the counters `okf.write.tokens` (attribute `okf.token.kind`: `prompt` or `completion`), `okf.write.pages` (attribute `okf.write.result`) and `okf.write.validation_failures`, labelled by source repo and bundle.

#### Scenario: Span and counters

- **WHEN** a write run finishes with in-memory exporters installed
- **THEN** the `write` span carries the page counts and the three counters carry the run's values
