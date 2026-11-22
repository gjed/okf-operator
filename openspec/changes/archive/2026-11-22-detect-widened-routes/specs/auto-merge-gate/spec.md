# Spec Delta

## ADDED Requirements

### Requirement: Routed paths in the provenance rule

The `provenance` rule SHALL NOT require the optional `routed_paths` key, and a `routed_paths` that is not a list of strings SHALL fail it.

#### Scenario: Page with or without routed paths

- **WHEN** one changed page carries `routed_paths` and another does not, and both are otherwise valid
- **THEN** the rule passes

#### Scenario: Routed paths of the wrong type

- **WHEN** a changed page has `routed_paths: src/a.go`
- **THEN** the rule fails and its details name the page and `routed_paths`
