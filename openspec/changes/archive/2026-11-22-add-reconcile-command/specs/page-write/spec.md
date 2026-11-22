# Spec Delta

## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Reconcile plan validation

A version 1 plan with a `reconcile` object, a version 2 plan without one, or a version 2 plan whose `reconcile.pages` do not match its pages one to one, name an unknown reason or evidence, give a `hunks` entry no full commit SHA `base` or give a `files` entry a `base`, SHALL be a runtime error before any GitHub or model call.

#### Scenario: Reconcile plan without page entries

- **WHEN** a version 2 plan has a page with no `reconcile.pages` entry
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
