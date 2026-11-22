# Spec Delta

## MODIFIED Requirements

### Requirement: Provenance rule

The `provenance` rule SHALL pass only when every changed page carries all five provenance fields (`source_repo`, `source_sha`, `source_paths`, `updated_at`, `generated_by`) as `internal/provenance` defines them, `source_repo` is the plan's source, `source_sha` is the plan's `to`, and `source_sha` exists in `source_repo`. The optional `routed_paths` key SHALL NOT be required, and a `routed_paths` of the wrong type SHALL fail the rule. A 404 for the commit, or the 422 "No commit found for SHA" that GitHub returns for an unknown commit, SHALL fail the rule. Any other API error SHALL fail the run.

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

#### Scenario: Page with or without routed paths

- **WHEN** one changed page carries `routed_paths` and another does not, and both are otherwise valid
- **THEN** the rule passes

#### Scenario: Routed paths of the wrong type

- **WHEN** a changed page has `routed_paths: src/a.go`
- **THEN** the rule fails and its details name the page and `routed_paths`
