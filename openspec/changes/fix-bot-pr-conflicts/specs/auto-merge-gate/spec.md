# Spec Delta

## ADDED Requirements

### Requirement: Mergeable rule

The `mergeable` rule SHALL fail when the PR's `mergeable_state` is `dirty`, naming the base branch in its details, and SHALL pass for every other state, including `unknown`. `gate` SHALL read the state with the PR, retrying while GitHub answers `unknown` at most four more times with a doubling delay starting at 500 ms.

#### Scenario: Conflicted PR

- **WHEN** the PR's `mergeable_state` is `dirty`
- **THEN** the `mergeable` rule fails and the report's `pass` is `false`

#### Scenario: Behind PR

- **WHEN** the PR's `mergeable_state` is `behind`
- **THEN** the `mergeable` rule passes

#### Scenario: State not computed

- **WHEN** the PR's `mergeable_state` stays `unknown` through every retry
- **THEN** the `mergeable` rule passes

## MODIFIED Requirements

### Requirement: JSON report

`gate` SHALL print one JSON report to stdout with `version` (2), `knowledge_repo`, `pr`, `head_sha`, `base_sha`, `plan_digest`, `pass` and `rules`. Each rule entry SHALL have `name`, `pass` and `details`, a sorted list of strings that is empty when the rule passes. Every rule SHALL be evaluated and reported even when an earlier rule fails, in the fixed order `provenance`, `routed_set`, `allowlist`, `change_shape`, `size`, `links`, `mergeable`, followed by `review` when `--review` is set.

#### Scenario: One rule fails

- **WHEN** only the size rule fails and `--review` is not set
- **THEN** the report lists all seven rules, only `size` has `pass: false`, and the top-level `pass` is `false`

#### Scenario: Review appended

- **WHEN** `gate` runs with `--review`
- **THEN** the report lists eight rules and the last one is `review`
