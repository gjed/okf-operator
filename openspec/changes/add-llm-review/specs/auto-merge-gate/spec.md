# Spec Delta

## ADDED Requirements

### Requirement: Review input

`gate` SHALL take an optional `--review <file>`, the JSON result (version 1) of `okf-operator review` for the same PR. When set, the report SHALL gain a `review` rule that passes only when the review result's `pass` is true, with one detail per failing page: `<path>: <reason>` for a page the model did not review, and `<path>: unsupported: <sentence>` for each unsupported sentence.

#### Scenario: Failing review

- **WHEN** every deterministic rule passes and the review result fails with an unsupported sentence
- **THEN** the `review` rule fails, its details name the page and the sentence, and `pass` is `false`

### Requirement: Review matches the PR

A review result with unknown fields or another version, or whose `pr` or `head_sha` differs from the PR under evaluation, SHALL fail the run (exit 1) and print no report.

#### Scenario: Review for another head

- **WHEN** `gate` runs with a review result whose `head_sha` differs from the PR's head
- **THEN** `gate` exits 1 and prints no report

## MODIFIED Requirements

### Requirement: JSON report

`gate` SHALL print one JSON report to stdout with `version` (1), `pr`, `head_sha`, `pass` and `rules`. Each rule entry SHALL have `name`, `pass` and `details`, a sorted list of strings that is empty when the rule passes. Every rule SHALL be evaluated and reported even when an earlier rule fails, in the fixed order `provenance`, `routed_set`, `allowlist`, `change_shape`, `size`, `links`, followed by `review` when `--review` is set.

#### Scenario: One rule fails

- **WHEN** only the size rule fails and `--review` is not set
- **THEN** the report lists all six rules, only `size` has `pass: false`, and the top-level `pass` is `false`

#### Scenario: Review appended

- **WHEN** `gate` runs with `--review`
- **THEN** the report lists seven rules and the last one is `review`

### Requirement: Later inputs can only block

The top-level `pass` SHALL be the conjunction of every entry in `rules`. A rule added later, including the LLM review, SHALL be reported as another entry and SHALL NOT be able to turn `pass` from `false` into `true`.

#### Scenario: Extra passing rule

- **WHEN** a report has a failing deterministic rule and a passing review entry
- **THEN** `pass` is `false`

#### Scenario: Failing review after passing rules

- **WHEN** every deterministic rule passes and the review entry fails
- **THEN** `pass` is `false`
