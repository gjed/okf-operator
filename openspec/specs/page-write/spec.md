# page-write Specification

## Purpose

Defines the `write` subcommand: given a plan, it asks a model for the updated content of each planned page, validates it, stamps provenance and writes the changed pages to a knowledge checkout. Its JSON report and the modified working tree are the input contract for `propose`.

## Requirements

### Requirement: Write inputs

`write` SHALL require `--plan <file|->` (a plan JSON document of version 1 or 2, `-` for stdin) and `--knowledge-dir <path>` (a checkout of the knowledge repo). It SHALL accept `--max-pages` (default 10) and `--max-tokens-per-page` (default 16000), the model flags from the `model-client` spec and the GitHub App flags from the `github-access` spec. A plan whose `version` is not `1` or `2`, or with unknown keys, SHALL be a runtime error.

#### Scenario: Plan from stdin

- **WHEN** the user runs `okf-operator plan ... | okf-operator write --plan - --knowledge-dir kb`
- **THEN** `write` reads the plan from stdin

#### Scenario: Missing knowledge dir

- **WHEN** `write` runs without `--knowledge-dir`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Unsupported plan version

- **WHEN** the plan document has `version: 3`
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

For each page, `write` SHALL send the model the compare patches of that page's `source_paths` only, compared from the plan's `from`, or for a version 2 page with evidence `hunks` from the page's `base`, to `to`, with one compare call per distinct base. When the patch of any of those paths is missing or truncated, the page SHALL be dropped with reason `patch_unavailable` and no model call SHALL be made for it.

#### Scenario: Truncated patch

- **WHEN** the compare API returns no `patch` for one of a page's source paths
- **THEN** the page is reported as dropped with reason `patch_unavailable` and the model receives no request for it

#### Scenario: Pages with different bases

- **WHEN** a version 2 plan has two `hunks` pages with bases `A` and `B`
- **THEN** each page's prompt holds the patches of `A..to` or `B..to` respectively

### Requirement: Source PR context is untrusted

For a version 1 plan, `write` SHALL include the titles and bodies of the PRs that introduced the commits in `from..to` in the prompt, inside a delimited block that the prompt declares untrusted data. Occurrences of the delimiter inside PR text, hunks or source files SHALL be neutralised so the block cannot be closed early. PR titles and bodies in one prompt SHALL total at most 16 KiB; the newest PRs are kept and the prompt states how many older ones were left out.

#### Scenario: Delimiter in a PR body

- **WHEN** a PR body contains the closing delimiter of the untrusted block
- **THEN** the prompt sent to the model contains exactly one closing delimiter for that block

#### Scenario: Many PRs in the range

- **WHEN** the range's PR text exceeds 16 KiB
- **THEN** the prompt holds the newest PRs that fit and a note with the number left out

#### Scenario: Delimiter in a source file

- **WHEN** a source file sent as whole-file evidence contains the closing delimiter of the untrusted block
- **THEN** the prompt sent to the model contains exactly one closing delimiter for that block

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

### Requirement: Page validation with one retry

Each model page SHALL be validated as an OKF concept document before it is accepted. On failure, `write` SHALL make one more model call that includes the validator error. If the second reply also fails, the page SHALL be dropped with reason `validation_failed`.

#### Scenario: Invalid page retried once then dropped

- **WHEN** the model returns an invalid page twice for the same page
- **THEN** exactly two model calls are made for it, the file is not modified, and the page is reported as dropped with reason `validation_failed`

#### Scenario: Retry succeeds

- **WHEN** the first reply is invalid and the second is valid
- **THEN** the page is reported as `changed` with `attempts: 2`

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

A page with `state: create` whose path exists under `--knowledge-dir`, as any file type, SHALL exit 1 before any GitHub or model call. `plan` and `write` SHALL therefore run against the same checkout: the open bot PR's branch for the source, otherwise the default branch, so a page created in an open bot PR is planned as `update`.

#### Scenario: Create path already exists

- **WHEN** the plan has a page with `state: create` whose path exists under `--knowledge-dir`
- **THEN** `write` exits 1 naming the page, and the model endpoint receives no request

#### Scenario: Page created in an open bot PR

- **WHEN** a bot PR that created a page is open, and `plan` and `write` both run against its branch
- **THEN** the plan lists the page as `update` and `write` updates it

### Requirement: Missing pages are dropped

Pages with `state: missing` SHALL NOT be sent to the model and SHALL be reported as dropped with reason `page_missing`. They SHALL NOT count against `--max-pages`.

#### Scenario: Missing page in the plan

- **WHEN** the plan has a page with `state: missing`
- **THEN** the report lists it as dropped with reason `page_missing` and no file is created

### Requirement: Reconcile plan validation

A version 1 plan with a `reconcile` object, a version 2 plan without one, or a version 2 plan whose `reconcile.pages` do not match its pages one to one, name an unknown reason or evidence, give a `hunks` entry no full commit SHA `base`, give a `files` entry a `base`, or give `routed_paths` that are not sorted, unique and non-empty strings, SHALL be a runtime error before any GitHub or model call. An entry without `routed_paths` SHALL be accepted.

#### Scenario: Reconcile plan without page entries

- **WHEN** a version 2 plan has a page with no `reconcile.pages` entry
- **THEN** `write` exits 1 before any GitHub or model call

#### Scenario: Unsorted routed paths

- **WHEN** a version 2 entry has `routed_paths: [src/b.go, src/a.go]`
- **THEN** `write` exits 1 before any GitHub or model call

### Requirement: No PR context for reconcile plans

The prompts of a version 2 plan SHALL carry no source PR titles or bodies, and `write` SHALL NOT list the PRs behind any commit for it.

#### Scenario: Reconcile prompt

- **WHEN** `write` runs a version 2 plan
- **THEN** no prompt holds a pull request block and GitHub receives no request for the PRs of a commit

### Requirement: Whole-file evidence

For each page with evidence `files` of a version 2 plan, `write` SHALL send the model the full content at `to` of each of the page's `source_paths`, inside the untrusted block, in place of patches. The budget, validation, provenance and working-tree requirements SHALL apply unchanged.

#### Scenario: Backfilled page from whole files

- **WHEN** a version 2 plan has a `create` page with evidence `files` and source path `docs/a.md`
- **THEN** the create prompt holds the content of `docs/a.md` at `to` inside the untrusted block and the created page has `source_sha` equal to `to`

### Requirement: Whole files are never partial

When any source file of a `files` page is missing at `to`, is not a regular file, is not valid UTF-8, contains a NUL byte or is larger than 64 KiB, the page SHALL be dropped with reason `file_unavailable` and no model call SHALL be made for it. A file SHALL never be sent in part.

#### Scenario: Source file too large

- **WHEN** one of a `files` page's source paths is 65 KiB at `to`
- **THEN** the page is reported as dropped with reason `file_unavailable` and the model receives no request for it

### Requirement: Routed paths from a reconcile entry

For a page whose version 2 `reconcile.pages` entry has `routed_paths`, `write` SHALL stamp that list as the page's `routed_paths`.

#### Scenario: Widened page written

- **WHEN** a version 2 page with reason `widened`, `source_paths: [src/b.go]` and entry `routed_paths: [src/a.go, src/b.go]` is changed
- **THEN** the written page has `routed_paths: [src/a.go, src/b.go]`

### Requirement: Routed paths carried forward

For any other page, `write` SHALL stamp as `routed_paths` the union of the current page's `routed_paths` and the page's `source_paths`, minus each path the plan's `changed_paths` report as `removed` and each `previous_path` of a `renamed` entry. A current page without `routed_paths` SHALL be stamped without it, and a created page SHALL record its `source_paths` minus those paths.

#### Scenario: Version 1 update of a page with routed paths

- **WHEN** a page records `routed_paths: [src/a.go, src/old.go]` and a version 1 plan renames `src/old.go` to `src/new.go`, with `source_paths: [src/new.go, src/old.go]`
- **THEN** the written page has `routed_paths: [src/a.go, src/new.go]`

#### Scenario: Version 1 update of a page without routed paths

- **WHEN** a page without `routed_paths` is changed from a version 1 plan
- **THEN** the written page has no `routed_paths`

#### Scenario: Version 1 create

- **WHEN** a version 1 plan creates a page from `source_paths: [docs/a.md]`
- **THEN** the created page has `routed_paths: [docs/a.md]`

### Requirement: Unchanged reconcile pages are re-stamped

For a version 2 plan, a page whose model reply is `NO_CHANGE`, or the current page unchanged apart from provenance keys, SHALL be reported `restamped`. Its provenance keys SHALL be stamped as for a changed page through `internal/provenance`, and its file replaced. The body SHALL be preserved byte for byte, and every other frontmatter key, its value, their order and the comments SHALL be kept. The frontmatter formatting, such as list indentation or blank lines, MAY be normalized by the encoder.

#### Scenario: Stale and widened pages answered NO_CHANGE

- **WHEN** a version 2 plan has one `stale` and one `widened` page and the model replies `NO_CHANGE` to both
- **THEN** both are reported `restamped`, each file has `source_sha` equal to the plan's `to` and the entry's `routed_paths`, and each body is byte-identical to before and every other key, its value, their order and the comments are kept

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
