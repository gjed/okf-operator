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

In App mode, the CLI SHALL mint an installation token for each repository owner it accesses at run time and SHALL NOT persist it. Installation tokens SHALL be requested with only the repositories and permissions the command needs.

#### Scenario: Plan permissions

- **WHEN** `plan` runs in App mode
- **THEN** it requests read-only `contents` (and `metadata`) for the source repo and for the knowledge repo, and nothing else

### Requirement: Private key never logged

The App private key and any token SHALL never appear in logs, span attributes or error messages, at any log level.

#### Scenario: Debug logging

- **WHEN** `plan` runs in App mode with `--log-level debug`
- **THEN** no log line or span attribute contains the key material or a token value
