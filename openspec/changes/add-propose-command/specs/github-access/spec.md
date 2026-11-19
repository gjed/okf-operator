# Spec Delta

## MODIFIED Requirements

### Requirement: Short-lived installation tokens

In App mode, the CLI SHALL mint an installation token for each repository owner it accesses at run time and SHALL NOT persist it. Installation tokens SHALL be requested with only the repositories and permissions the command needs.

#### Scenario: Plan permissions

- **WHEN** `plan` runs in App mode
- **THEN** it requests read-only `contents` (and `metadata`) for the source repo and for the knowledge repo, and nothing else

#### Scenario: Write permissions

- **WHEN** `write` runs in App mode
- **THEN** it requests read-only `contents`, `metadata` and `pull_requests` for the source repo only, and no token for the knowledge repo

#### Scenario: Propose permissions

- **WHEN** `propose` runs in App mode
- **THEN** it requests one token for the knowledge repo only, with `contents` and `pull_requests` write and `metadata` read, and one token for the source repo only, with `contents`, `pull_requests` and `metadata` read, even when both repos have the same owner
