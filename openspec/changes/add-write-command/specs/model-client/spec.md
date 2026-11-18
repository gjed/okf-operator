# Spec Delta

## Purpose

Defines how `okf-operator` calls a language model: one OpenAI-compatible chat completions endpoint, configured at run time, that receives text and returns text and never sees GitHub credentials.

## ADDED Requirements

### Requirement: OpenAI-compatible endpoint

The model client SHALL call `POST <base-url>/chat/completions` with the configured model name, a system and a user message, `temperature: 0` and `max_tokens`. The base URL SHALL come from `--model-base-url` or `OKF_MODEL_BASE_URL` and the model name from `--model` or `OKF_MODEL`; both are required for `write`. The API key SHALL come only from `OKF_MODEL_API_KEY`; when set it is sent as `Authorization: Bearer`, when unset no `Authorization` header is sent.

#### Scenario: Local endpoint without a key

- **WHEN** `write` runs with `OKF_MODEL_BASE_URL=http://localhost:11434/v1`, `OKF_MODEL=m1` and no API key
- **THEN** requests go to `http://localhost:11434/v1/chat/completions` with no `Authorization` header

#### Scenario: Missing model configuration

- **WHEN** `write` runs without a model base URL
- **THEN** it exits 2 and the error names `--model-base-url` and `OKF_MODEL_BASE_URL`

### Requirement: Text in, text out

The client SHALL return only the reply text, the finish reason and the token usage. It SHALL NOT execute tools or function calls, and SHALL NOT receive or send GitHub credentials.

#### Scenario: No GitHub token reaches the model endpoint

- **WHEN** `write` runs with `GITHUB_TOKEN` set
- **THEN** no request to the model endpoint carries the token in a header or in the body

### Requirement: Model API key never logged

The model API key SHALL never appear in logs, span attributes, error messages or the write report, at any log level, including when the endpoint returns an error that echoes the request.

#### Scenario: Endpoint error echoes the key

- **WHEN** the model endpoint replies 401 with a body containing the API key, with `--log-level debug`
- **THEN** stderr, the recorded spans and stdout contain no occurrence of the key
