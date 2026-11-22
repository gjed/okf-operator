# GitHub Access

## ADDED Requirements

### Requirement: Controller credentials from Secrets

The controller SHALL read each App's ID and private key from a Kubernetes Secret with the keys `app-id` and `private-key`, SHALL keep the key in memory only, and SHALL mint installation tokens with the same per-stage permissions as the CLI. It SHALL read the review App Secret only for the review, approve and dismissal steps, and SHALL resolve each App's bot login from `GET /app`.

#### Scenario: Write path

- **WHEN** a run is in its checkout, plan, write or propose stage
- **THEN** the review App Secret has not been read

#### Scenario: Missing Secret

- **WHEN** the writer App Secret does not exist
- **THEN** the run fails at stage `credentials` and no GitHub call is made
