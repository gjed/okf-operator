# Spec Delta

## ADDED Requirements

### Requirement: Separate reviewer model

`review` SHALL use the same client with its own configuration: the base URL from `--review-model-base-url` or `OKF_REVIEW_MODEL_BASE_URL`, the model name from `--review-model` or `OKF_REVIEW_MODEL`, both required, and the API key only from `OKF_REVIEW_MODEL_API_KEY`. `review` SHALL NOT fall back to `OKF_MODEL_BASE_URL`, `OKF_MODEL` or `OKF_MODEL_API_KEY`, and `write` SHALL NOT read the reviewer settings.

#### Scenario: Writer settings only

- **WHEN** `review` runs with `OKF_MODEL_BASE_URL` and `OKF_MODEL` set but no reviewer settings
- **THEN** it exits 2 and the error names `--review-model-base-url` and `OKF_REVIEW_MODEL_BASE_URL`

#### Scenario: Reviewer key sent to the reviewer only

- **WHEN** `review` runs with `OKF_REVIEW_MODEL_API_KEY` and `OKF_MODEL_API_KEY` set to different values
- **THEN** reviewer requests carry `Authorization: Bearer` with the reviewer key and never the writer key

## MODIFIED Requirements

### Requirement: Model API key never logged

The model API keys (`OKF_MODEL_API_KEY` and `OKF_REVIEW_MODEL_API_KEY`) SHALL never appear in logs, span attributes, error messages, the write report or the review result, at any log level, including when the endpoint returns an error that echoes the request.

#### Scenario: Endpoint error echoes the key

- **WHEN** the model endpoint replies 401 with a body containing the API key, with `--log-level debug`
- **THEN** stderr, the recorded spans and stdout contain no occurrence of the key

#### Scenario: Reviewer endpoint echoes the key

- **WHEN** `review` runs in App mode with `--log-level debug` and the reviewer endpoint replies 401 with a body containing the reviewer key
- **THEN** stderr, the recorded spans and stdout contain no occurrence of the reviewer key or of any GitHub token
