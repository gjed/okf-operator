# Spec Delta

## ADDED Requirements

### Requirement: Unchanged reconcile pages are re-stamped

For a version 2 plan, a page whose model reply is `NO_CHANGE`, or the current page unchanged apart from provenance keys, SHALL be reported `restamped`. Its provenance keys SHALL be stamped as for a changed page through `internal/provenance`, and its file replaced. The body, every other frontmatter key, their order and the comments SHALL be preserved byte for byte.

#### Scenario: Stale and widened pages answered NO_CHANGE

- **WHEN** a version 2 plan has one `stale` and one `widened` page and the model replies `NO_CHANGE` to both
- **THEN** both are reported `restamped`, each file has `source_sha` equal to the plan's `to` and the entry's `routed_paths`, and each body, every other key, their order and the comments are byte-identical to before

#### Scenario: Second reconcile from the advanced state

- **WHEN** `reconcile` runs again on the checkout after those pages were re-stamped
- **THEN** it plans neither page and no model call is made for them

#### Scenario: Page that cannot take provenance

- **WHEN** a version 2 plan's page has no closed frontmatter and the model replies `NO_CHANGE`
- **THEN** the reply counts as an invalid page and the model is asked again, so the page is never left planned on every reconcile

### Requirement: Version 1 plans do not re-stamp

A version 1 plan SHALL NOT re-stamp: an unchanged page is reported `unchanged` and its file is not modified. For a version 2 plan, a page whose stamped bytes equal the current file SHALL be reported `unchanged` and its file SHALL NOT be modified.

#### Scenario: Version 1 plan

- **WHEN** a version 1 plan's page has older provenance and the model replies `NO_CHANGE`
- **THEN** the page is reported `unchanged` and its file is not modified

#### Scenario: Re-stamp changes nothing

- **WHEN** the stamped page equals the current file byte for byte
- **THEN** the page is reported `unchanged` and its file is not modified

## MODIFIED Requirements

### Requirement: Model response contract

The model SHALL reply with either the literal `NO_CHANGE` or the complete updated page (frontmatter and body). A reply wrapped in one Markdown code fence SHALL be unwrapped. A reply that is neither, that ends because of the token limit, that contains a prompt block marker line, whose frontmatter `type` differs from the current page's, or that drops a top-level frontmatter key of the current page other than the provenance keys, SHALL count as an invalid page.

#### Scenario: Explicit no change

- **WHEN** the model replies `NO_CHANGE` for a page of a version 1 plan
- **THEN** the page is reported as `unchanged` and its file is not modified

#### Scenario: Explicit no change in a reconcile plan

- **WHEN** the model replies `NO_CHANGE` for a page of a version 2 plan
- **THEN** the page is reported as `restamped` and only its provenance keys change, see "Unchanged reconcile pages are re-stamped"

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

- **WHEN** the model returns the current page unchanged apart from provenance keys for a page of a version 1 plan
- **THEN** the page is reported as `unchanged` and its file, including provenance, is not modified

### Requirement: JSON write report

On success, `write` SHALL print one JSON document to stdout with `version: 3`, `source`, `bundle`, `from`, `to`, `model`, `generated_by`, `pages` and `tokens` (run totals). Each page entry SHALL carry `path`, `result` (`changed`, `created`, `restamped`, `unchanged` or `dropped`), `reason` (empty unless dropped), `attempts` and `tokens` (`prompt`, `completion`). `pages` SHALL list every plan page sorted by path.

#### Scenario: Mixed results

- **WHEN** a run changes one page, creates one, re-stamps one, leaves one unchanged and drops one
- **THEN** the report lists all five sorted by path with their results and reasons

### Requirement: Propose input contract

The write report and the files under `--knowledge-dir` SHALL be the input contract for `propose`: exactly the pages with result `changed` or `restamped` are modified, exactly the pages with result `created` are new regular files, and no other file is created, modified or deleted.

#### Scenario: Working tree matches the report

- **WHEN** a run reports one page `changed`, one `created`, one `restamped`, one `unchanged` and one `dropped`
- **THEN** `git status --porcelain` in the checkout lists only the changed, restamped and created pages

#### Scenario: Empty plan

- **WHEN** the plan has no pages
- **THEN** `write` exits 0, makes no GitHub or model call, and prints a report with an empty `pages` list and zero tokens

### Requirement: Write telemetry

Each `write` run SHALL annotate the dispatcher's `write` span with the source repo, bundle, model and the counts of changed, created, restamped, unchanged and dropped pages, and SHALL record the counters `okf.write.tokens` (attribute `okf.token.kind`: `prompt` or `completion`), `okf.write.pages` (attribute `okf.write.result`: `changed`, `created`, `restamped`, `unchanged` or `dropped`) and `okf.write.validation_failures`, labelled by source repo and bundle.

#### Scenario: Span and counters

- **WHEN** a write run finishes with in-memory exporters installed
- **THEN** the `write` span carries the page counts and the three counters carry the run's values

#### Scenario: Restamped page counted

- **WHEN** a run re-stamps one page
- **THEN** `okf.write.pages` with `okf.write.result=restamped` is 1 and the span attribute `okf.write.pages.restamped` is 1
