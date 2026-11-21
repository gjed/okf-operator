# review-approval Specification

## Purpose

Defines `okf-operator approve` and the review-only GitHub App it signs in as, so that a knowledge PR that passes every gate rule, including the LLM review, gets an approval from an identity other than its author.

## Requirements

### Requirement: Approve inputs

`approve` SHALL take `--knowledge-repo owner/name`, `--pr <number>` and `--report <file|->`, where `--report` is a `gate` report (version 1) and `-` reads it from stdin. A report with unknown fields or another version SHALL exit 1. A report whose `pr` differs from `--pr` SHALL exit 1. A missing or malformed flag SHALL exit 2.

#### Scenario: Report for another PR

- **WHEN** `approve` runs with `--pr 7` and a report with `pr: 8`
- **THEN** it exits 1 and posts nothing

### Requirement: Approve only a passing, reviewed, current head

`approve` SHALL post an approving pull request review only when the report's `pass`, recomputed as the conjunction of its rules, is true, the report contains a `review` rule that passes, and the PR's current head SHA equals the report's `head_sha`. The review SHALL be posted for that commit. Otherwise it SHALL post nothing.

#### Scenario: Gate failed

- **WHEN** the report has `pass: false`
- **THEN** nothing is posted and the outcome has `approved: false` and `reason: "gate_failed"`

#### Scenario: No review in the report

- **WHEN** every rule passes but the report has no `review` rule
- **THEN** nothing is posted and the outcome has `reason: "review_missing"`

#### Scenario: Head moved

- **WHEN** the PR head differs from the report's `head_sha`
- **THEN** nothing is posted and the outcome has `reason: "head_moved"`

#### Scenario: Approval

- **WHEN** the report passes with a passing `review` rule and the head is unchanged
- **THEN** one `APPROVE` review is posted on the report's `head_sha` and the outcome has `approved: true`

### Requirement: Complete report only

`approve` SHALL post nothing unless the report contains every deterministic gate rule: `provenance`, `routed_set`, `allowlist`, `change_shape`, `size` and `links`. A report missing one SHALL yield `reason: "report_incomplete"`, so removing a failing rule from a report cannot produce an approval.

#### Scenario: Rule removed from the report

- **WHEN** a report passes but has no `size` rule
- **THEN** nothing is posted and the outcome has `reason: "report_incomplete"`

### Requirement: Stale approvals dismissed

The documentation SHALL state that the knowledge repo's branch protection or ruleset must dismiss stale approvals when new commits are pushed, because an approval posted on one head otherwise keeps counting after `propose` pushes another.

#### Scenario: Setup instructions

- **WHEN** an operator follows the review App setup in `docs/github-app.md`
- **THEN** it tells them to enable dismissal of stale approvals on push for the knowledge repo

### Requirement: Approve outcome

`approve` SHALL print one JSON outcome with `version` (1), `pr`, `head_sha`, `approved` and `reason` (empty when approved) and exit 0 whether or not it approved. A GitHub error SHALL exit 1.

#### Scenario: Outcome on stdout

- **WHEN** `approve` declines because the gate failed
- **THEN** it exits 0 and prints the outcome

### Requirement: Review-only identity

`approve` SHALL authenticate only with the review App: `--review-app-id` and `--review-app-private-key-file`, or `OKF_REVIEW_GITHUB_APP_ID` and `OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE`, or else `OKF_REVIEW_GITHUB_TOKEN`. It SHALL NOT read `OKF_GITHUB_APP_ID`, `OKF_GITHUB_APP_PRIVATE_KEY_FILE` or `GITHUB_TOKEN`. No other command SHALL read the review App's settings. With no review credentials, `approve` SHALL exit 2 and name the review options.

#### Scenario: Writer credentials only

- **WHEN** `approve` runs with `GITHUB_TOKEN` and `OKF_GITHUB_APP_ID` set but no review credentials
- **THEN** it exits 2 and the error names `--review-app-id` and `OKF_REVIEW_GITHUB_TOKEN`

#### Scenario: Writer never loads review credentials

- **WHEN** `write` or `propose` runs with `OKF_REVIEW_GITHUB_APP_ID` and `OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE` set and no other credentials
- **THEN** it exits 2 for missing credentials

### Requirement: Review App manifest

The repository SHALL ship `deploy/review-app-manifest.json`, a GitHub App manifest that requests only `pull_requests: write`, `contents: read` and `metadata: read`, no webhook and no events.

#### Scenario: Minimal permissions

- **WHEN** the manifest is parsed
- **THEN** its `default_permissions` are exactly those three and `default_events` is empty

### Requirement: Approve telemetry

`approve` SHALL set `okf.approve.pr`, `okf.approve.head_sha`, `okf.approve.approved` and `okf.approve.reason` on the dispatcher's `approve` span.

#### Scenario: Declined approval

- **WHEN** `approve` declines because the head moved
- **THEN** the `approve` span has `okf.approve.approved` false and `okf.approve.reason` `head_moved`
