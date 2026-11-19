# Spec Delta

## Purpose

Defines `.kb/policy.yaml`, the knowledge repo's auto-merge policy: which concept types and bundles may auto-merge and how large a generated change may be.

## ADDED Requirements

### Requirement: Policy location and version

The policy SHALL be read from `.kb/policy.yaml` in the knowledge repo. The file SHALL carry `version: 1`. Any other version SHALL fail loading with an error that names the found and the supported version.

#### Scenario: Unsupported version

- **WHEN** `.kb/policy.yaml` contains `version: 2`
- **THEN** loading fails and the error names version 2 and the supported version 1

#### Scenario: Missing policy

- **WHEN** the knowledge repo has no `.kb/policy.yaml`
- **THEN** `gate` exits 1 with an error that names the missing file, and prints no report

### Requirement: Strict decoding

Unknown keys anywhere in the policy SHALL fail loading.

#### Scenario: Unknown key

- **WHEN** the policy contains `auto_merge: {typs: [Service]}`
- **THEN** loading fails and the error names the unknown key

### Requirement: Auto-merge allowlist

The policy SHALL list the concept types (`auto_merge.types`) and the bundles (`auto_merge.bundles`) allowed to auto-merge. Both lists SHALL default to empty, and an empty list SHALL allow nothing. Entries SHALL be non-empty and unique, and bundle entries SHALL be relative directories as in `routing.yaml`.

#### Scenario: Unconfigured allowlist

- **WHEN** the policy omits `auto_merge`
- **THEN** loading succeeds and no page passes the allowlist rule

#### Scenario: Invalid bundle entry

- **WHEN** `auto_merge.bundles` contains `../other`
- **THEN** loading fails and the error names the entry

### Requirement: Diff size limits

The policy SHALL set `limits.page_lines` and `limits.pr_lines`, both positive integers. A missing or non-positive limit SHALL fail loading.

#### Scenario: Missing limit

- **WHEN** the policy omits `limits.pr_lines`
- **THEN** loading fails and the error names `limits.pr_lines`

### Requirement: All validation errors reported

Validation SHALL check every rule and report every violation in one error.

#### Scenario: Several problems

- **WHEN** the policy has a duplicate type and a zero `page_lines`
- **THEN** the error lists both problems
