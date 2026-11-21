# Spec Delta

## ADDED Requirements

### Requirement: Page creation

Pages with `state: create` SHALL be created from the template of the route `type` at `.kb/templates/<type>.md`. When that template does not exist, the page SHALL be dropped with reason `template_missing` and no model call SHALL be made for it. The create prompt SHALL hold the writing guidance, the template, up to two reference pages of the route `type`, the page's hunks and the untrusted PR block. `write` SHALL NOT edit the bundle `index.md` to link a created page.

#### Scenario: Page created from its template

- **WHEN** the plan has a page with `state: create` and `type: concept`, `.kb/templates/concept.md` exists and the model replies with a valid page of `type: concept`
- **THEN** the file is created with the model's content plus stamped provenance, the page is reported as `created`, and the prompt held the template

#### Scenario: Route type without a template

- **WHEN** the plan has a page with `state: create` and `type: runbook`, and `.kb/templates/runbook.md` does not exist
- **THEN** the page is reported as dropped with reason `template_missing`, no file is created and the model receives no request for it

### Requirement: Create response contract

For a page with `state: create`, the reply SHALL be the complete new page. `NO_CHANGE`, a frontmatter `type` that differs from the route `type`, or a missing top-level frontmatter key of the template other than the provenance keys SHALL count as an invalid page, in addition to the rules of the model response contract that do not refer to a current page.

#### Scenario: Created page has another type

- **WHEN** a create page's route `type` is `concept` and the reply has `type: guide`
- **THEN** the reply counts as an invalid page

#### Scenario: No change for a create

- **WHEN** the model replies `NO_CHANGE` for a page with `state: create`
- **THEN** the reply counts as an invalid page

### Requirement: Create targets a new path

A page with `state: create` whose path exists under `--knowledge-dir`, as any file type, SHALL exit 1 before any GitHub or model call.

#### Scenario: Create path already exists

- **WHEN** the plan has a page with `state: create` whose path exists under `--knowledge-dir`
- **THEN** `write` exits 1 naming the page, and the model endpoint receives no request

### Requirement: Missing pages are dropped

Pages with `state: missing` SHALL NOT be sent to the model and SHALL be reported as dropped with reason `page_missing`. They SHALL NOT count against `--max-pages`.

#### Scenario: Missing page in the plan

- **WHEN** the plan has a page with `state: missing`
- **THEN** the report lists it as dropped with reason `page_missing` and no file is created

## MODIFIED Requirements

### Requirement: Model response contract

The model SHALL reply with either the literal `NO_CHANGE` or the complete updated page (frontmatter and body). A reply wrapped in one Markdown code fence SHALL be unwrapped. A reply that is neither, that ends because of the token limit, that contains a prompt block marker line, whose frontmatter `type` differs from the current page's, or that drops a top-level frontmatter key of the current page other than the provenance keys, SHALL count as an invalid page.

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

### Requirement: Hard budget

Before the first model call, `write` SHALL check that the `update` and `create` pages do not exceed `--max-pages` and that every page's estimated first-attempt prompt plus its current page (template for a create) fits in `--max-tokens-per-page`. Either failure SHALL exit 1 with no model call. `--max-tokens-per-page` SHALL bound the total tokens of all attempts for one page; a retry that would exceed it SHALL NOT be made and the page SHALL be dropped with reason `validation_failed`.

#### Scenario: Budget exceeded

- **WHEN** the plan has 3 `update` pages and `--max-pages 2` is set
- **THEN** `write` exits 1, the error names the limit, and the model endpoint receives no request

#### Scenario: Creates count against the page budget

- **WHEN** the plan has 1 `update` page, 1 `create` page and `--max-pages 1` is set
- **THEN** `write` exits 1 and the model endpoint receives no request

#### Scenario: Page too large for the token budget

- **WHEN** one page's estimated prompt exceeds `--max-tokens-per-page`
- **THEN** `write` exits 1 before any model call and names the page

### Requirement: All-or-nothing working tree

`write` SHALL modify files under `--knowledge-dir` only after every planned page has been processed. When the run exits non-zero, no file under `--knowledge-dir` SHALL have been modified or created. Each changed page SHALL be replaced through a temporary file and a rename, so no page is ever left half-written. Each created page SHALL be written to a temporary file and linked to its name with an operation that fails when the name exists, so a create never overwrites a file.

#### Scenario: Model endpoint fails mid-run

- **WHEN** the model endpoint returns HTTP 500 for the second of three pages
- **THEN** `write` exits 1 and no file under `--knowledge-dir` is modified or created

### Requirement: JSON write report

On success, `write` SHALL print one JSON document to stdout with `version: 2`, `source`, `bundle`, `from`, `to`, `model`, `generated_by`, `pages` and `tokens` (run totals). Each page entry SHALL carry `path`, `result` (`changed`, `created`, `unchanged` or `dropped`), `reason` (empty unless dropped), `attempts` and `tokens` (`prompt`, `completion`). `pages` SHALL list every plan page sorted by path.

#### Scenario: Mixed results

- **WHEN** a run changes one page, creates one, leaves one unchanged and drops one
- **THEN** the report lists all four sorted by path with their results and reasons

### Requirement: Propose input contract

The write report and the files under `--knowledge-dir` SHALL be the input contract for `propose`: exactly the pages with result `changed` are modified, exactly the pages with result `created` are new regular files, and no other file is created, modified or deleted.

#### Scenario: Working tree matches the report

- **WHEN** a run reports one page `changed`, one `created`, one `unchanged` and one `dropped`
- **THEN** `git status --porcelain` in the checkout lists only the changed page and the created page

#### Scenario: Empty plan

- **WHEN** the plan has no pages
- **THEN** `write` exits 0, makes no GitHub or model call, and prints a report with an empty `pages` list and zero tokens

### Requirement: Write telemetry

Each `write` run SHALL annotate the dispatcher's `write` span with the source repo, bundle, model and the counts of changed, created, unchanged and dropped pages, and SHALL record the counters `okf.write.tokens` (attribute `okf.token.kind`: `prompt` or `completion`), `okf.write.pages` (attribute `okf.write.result`: `changed`, `created`, `unchanged` or `dropped`) and `okf.write.validation_failures`, labelled by source repo and bundle.

#### Scenario: Span and counters

- **WHEN** a write run finishes with in-memory exporters installed
- **THEN** the `write` span carries the page counts and the three counters carry the run's values

## REMOVED Requirements

### Requirement: Page creation is out of scope

**Reason**: `write` now creates pages for `state: create`.

**Migration**: see "Page creation". `state: missing` pages keep the `page_missing` drop; the `create_unsupported` reason no longer exists.
