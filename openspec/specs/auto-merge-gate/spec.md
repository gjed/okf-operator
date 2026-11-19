# auto-merge-gate Specification

## Purpose

Defines `okf-operator gate`: deterministic rules that decide whether a knowledge PR may auto-merge, and the JSON report that carries the verdict.

## Requirements

### Requirement: Gate inputs

`gate` SHALL take `--knowledge-repo owner/name`, `--pr <number>` and `--plan <file|->`, where `--plan` is the plan JSON (version 1) of the run that produced the PR and `-` reads it from stdin. It SHALL read the PR, its changed files and their contents through the GitHub API and SHALL NOT need a local checkout. A missing or malformed flag SHALL exit 2.

#### Scenario: Missing PR number

- **WHEN** `gate` runs without `--pr`
- **THEN** it exits 2 and prints the `gate` flags to stderr

#### Scenario: Plan with unknown fields

- **WHEN** the plan JSON has `version: 2` or an unknown field
- **THEN** `gate` exits 1 and prints no report

### Requirement: Configuration from the base branch

`gate` SHALL read `.kb/policy.yaml` and `.kb/routing.yaml` from the PR's base branch, resolved once to a commit at the start of the run. It SHALL NOT read either file from the PR head. The plan's source SHALL be onboarded in that routing map with the plan's bundle, or the run fails.

#### Scenario: Head rewrites the policy

- **WHEN** the PR head changes `.kb/policy.yaml` to allow its own pages
- **THEN** the rules are evaluated with the base branch policy, the change-shape rule fails, and `pass` is `false`

#### Scenario: Plan bundle mismatch

- **WHEN** the plan's bundle differs from the bundle the base routing map binds to the plan's source
- **THEN** `gate` exits 1 and prints no report

### Requirement: Contents read at the head commit

Changed page contents SHALL be read at the PR's head SHA, recorded once at the start of the run, so a push during the run cannot mix two heads in one verdict. The changed file list has no ref parameter, so the PR SHALL be re-read after listing it, and a head that moved SHALL fail the run.

#### Scenario: Head SHA in the report

- **WHEN** `gate` evaluates a PR
- **THEN** the report's `head_sha` is the commit every head file was read at

#### Scenario: Push during the listing

- **WHEN** the PR head changes between reading the PR and listing its files
- **THEN** `gate` exits 1 and prints no report

### Requirement: Provenance rule

The `provenance` rule SHALL pass only when every changed page carries all five provenance fields (`source_repo`, `source_sha`, `source_paths`, `updated_at`, `generated_by`) as `internal/provenance` defines them, `source_repo` is the plan's source, `source_sha` is the plan's `to`, and `source_sha` exists in `source_repo`. A 404 for the commit, or the 422 "No commit found for SHA" that GitHub returns for an unknown commit, SHALL fail the rule. Any other API error SHALL fail the run.

#### Scenario: Missing field

- **WHEN** a changed page has no `generated_by`
- **THEN** the rule fails and its details name the page and the field

#### Scenario: Commit other than the plan's to

- **WHEN** a changed page's `source_sha` exists in `source_repo` but differs from the plan's `to`
- **THEN** the rule fails and its details name the page, the SHA and the plan's `to`

#### Scenario: Unknown commit

- **WHEN** a changed page's `source_sha` returns 404, or 422 "No commit found for SHA", in `source_repo`
- **THEN** the rule fails and its details name the page and the SHA

#### Scenario: Source repo unavailable

- **WHEN** the commit lookup returns 500
- **THEN** `gate` exits 1 and prints no report

### Requirement: Routed set rule

The `routed_set` rule SHALL pass only when every changed file is one of the plan's `pages[].path`.

#### Scenario: Unplanned file

- **WHEN** the PR changes a page that is not in the plan
- **THEN** the rule fails and its details name the file

### Requirement: Allowlist rule

The `allowlist` rule SHALL pass only when the plan's bundle is in `auto_merge.bundles` and the `type` of every changed page is in `auto_merge.types`. The type SHALL be read from the page at the base commit, and a PR that changes a page's `type` SHALL fail the rule.

#### Scenario: Type not allowed

- **WHEN** a changed page has a type that is not in `auto_merge.types`
- **THEN** the rule fails and its details name the page and the type

#### Scenario: Empty allowlist

- **WHEN** the policy has no `auto_merge` entries
- **THEN** the rule fails

### Requirement: Change-shape rule

The `change_shape` rule SHALL pass only when every changed file has status `modified` and no changed file is under `.kb/`. Added, removed, renamed, copied and mode-only changes SHALL fail the rule.

#### Scenario: New page

- **WHEN** the PR adds a page
- **THEN** the rule fails and its details name the file and its status

#### Scenario: Config change

- **WHEN** the PR modifies `.kb/routing.yaml`
- **THEN** the rule fails and its details name the file

### Requirement: Size rule

The `size` rule SHALL count, for each changed file, the added plus removed lines reported by the pull request files API. It SHALL fail when any file exceeds `limits.page_lines` or the sum exceeds `limits.pr_lines`.

#### Scenario: Page over the limit

- **WHEN** a changed page has 120 changed lines and `page_lines` is 100
- **THEN** the rule fails and its details name the page, its count and the limit

#### Scenario: PR over the limit

- **WHEN** every page is under `page_lines` but the sum exceeds `pr_lines`
- **THEN** the rule fails and its details give the sum and the limit

### Requirement: Links rule

The `links` rule SHALL pass only when every relative link in every changed page resolves, relative to the page, to an existing file inside the plan's bundle at the PR head. Links with a URL scheme, protocol-relative links and fragment-only links SHALL be ignored. A fragment or query on a relative link SHALL be dropped before resolving.

#### Scenario: Broken link

- **WHEN** a changed page links to `../missing.md` and that file does not exist at the head
- **THEN** the rule fails and its details name the page and the link

#### Scenario: Link escapes the bundle

- **WHEN** a changed page links to a file outside the bundle
- **THEN** the rule fails even when that file exists

### Requirement: JSON report

`gate` SHALL print one JSON report to stdout with `version` (1), `pr`, `head_sha`, `pass` and `rules`. Each rule entry SHALL have `name`, `pass` and `details`, a sorted list of strings that is empty when the rule passes. Every rule SHALL be evaluated and reported even when an earlier rule fails, in the fixed order `provenance`, `routed_set`, `allowlist`, `change_shape`, `size`, `links`.

#### Scenario: One rule fails

- **WHEN** only the size rule fails
- **THEN** the report lists all six rules, only `size` has `pass: false`, and the top-level `pass` is `false`

### Requirement: Later inputs can only block

The top-level `pass` SHALL be the conjunction of every entry in `rules`. A rule added later, including the LLM review, SHALL be reported as another entry and SHALL NOT be able to turn `pass` from `false` into `true`.

#### Scenario: Extra passing rule

- **WHEN** a report has a failing deterministic rule and a passing review entry
- **THEN** `pass` is `false`

### Requirement: Deterministic output

The same PR head, base commit, plan and policy SHALL produce a byte-identical report.

#### Scenario: Repeated run

- **WHEN** `gate` runs twice against the same inputs
- **THEN** both reports are byte-identical

### Requirement: Exit codes

A completed evaluation SHALL exit 0 whether `pass` is `true` or `false`, so callers branch on the report. A run that cannot complete the evaluation SHALL exit 1 and print no report. A usage error SHALL exit 2.

#### Scenario: Failed gate

- **WHEN** a rule fails
- **THEN** `gate` prints the report with `pass: false` and exits 0

### Requirement: Gate telemetry

`gate` SHALL set its attributes on the dispatcher's `gate` span and SHALL add one `okf.gate.rule_results` count per rule with the attributes `okf.gate.rule` and `okf.gate.outcome` (`pass` or `fail`).

#### Scenario: Rule counters

- **WHEN** `gate` completes with five passing rules and one failing rule
- **THEN** `okf.gate.rule_results` gains five `pass` and one `fail` data points, one per rule name
