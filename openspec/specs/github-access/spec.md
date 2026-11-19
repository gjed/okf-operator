# github-access Specification

## Purpose

Defines how `okf-operator` authenticates to GitHub, so that it runs locally with a personal token and in production as a GitHub App with short-lived, minimally scoped credentials.

## Requirements

### Requirement: Credential sources

The CLI SHALL authenticate with a GitHub App when `--app-id` (or `OKF_GITHUB_APP_ID`) and `--app-private-key-file` (or `OKF_GITHUB_APP_PRIVATE_KEY_FILE`) are set. Otherwise it SHALL use `GITHUB_TOKEN`. With neither set, commands that call GitHub SHALL exit 2 and name both options.

#### Scenario: Token fallback

- **WHEN** no App settings are present and `GITHUB_TOKEN` is set
- **THEN** GitHub calls use that token

#### Scenario: No credentials

- **WHEN** neither App settings nor `GITHUB_TOKEN` are present
- **THEN** `plan` exits 2 and the error names both credential options

### Requirement: Short-lived installation tokens

In App mode, the CLI SHALL mint an installation token for each repository owner and permission set it accesses at run time and SHALL NOT persist it. Installation tokens SHALL be requested with only the repositories and permissions the command needs.

#### Scenario: Plan permissions

- **WHEN** `plan` runs in App mode
- **THEN** it requests read-only `contents` (and `metadata`) for the source repo and for the knowledge repo, and nothing else

#### Scenario: Write permissions

- **WHEN** `write` runs in App mode
- **THEN** it requests read-only `contents`, `metadata` and `pull_requests` for the source repo only, and no token for the knowledge repo

#### Scenario: Propose permissions

- **WHEN** `propose` runs in App mode
- **THEN** it requests one token for the knowledge repo only, with `contents` and `pull_requests` write and `metadata` read, and one token for the source repo only, with `contents`, `pull_requests` and `metadata` read, even when both repos have the same owner

#### Scenario: Gate permissions

- **WHEN** `gate` runs in App mode
- **THEN** it requests one token for the knowledge repo with read-only `contents`, `metadata` and `pull_requests`, and one token for the plan's source repo with read-only `contents` and `metadata`, and nothing else

### Requirement: Private key never logged

The App private key and any token SHALL never appear in logs, span attributes or error messages, at any log level.

#### Scenario: Debug logging

- **WHEN** `plan` runs in App mode with `--log-level debug`
- **THEN** no log line or span attribute contains the key material or a token value
