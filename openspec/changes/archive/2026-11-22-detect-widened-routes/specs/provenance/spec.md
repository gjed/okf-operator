# Spec Delta

## ADDED Requirements

### Requirement: Optional routed paths

A generated page MAY carry a sixth provenance key, `routed_paths`: the sorted list of source files the page reflects at `source_sha`. A page without it, or with a null value, SHALL be treated as having an unknown routed set.

#### Scenario: Page stamped before routed paths existed

- **WHEN** a page carries the five provenance keys and no `routed_paths`
- **THEN** it parses without error and its routed set is unknown

### Requirement: Routed paths parsing

`Parse` SHALL tell an unknown routed set (absent or null `routed_paths`) from a list, an empty list included. `Stamp` SHALL write a known set and remove the key for an unknown one. Both SHALL return an error for a `routed_paths` that is not a list of strings.

#### Scenario: Routed paths of the wrong type

- **WHEN** a page has `routed_paths: src/a.go`
- **THEN** `Parse` and `Stamp` return an error

#### Scenario: Empty routed paths

- **WHEN** a page is stamped with an empty routed set and parsed
- **THEN** `Parse` returns an empty, known routed set

## MODIFIED Requirements

### Requirement: Tool-owned provenance

Provenance SHALL be stamped by the tool after the model returns. Any provenance key in the model's output SHALL be overwritten, and a `routed_paths` key SHALL be removed when the tool stamps an unknown routed set. All other frontmatter keys, their order and the body SHALL be preserved.

#### Scenario: Model writes a provenance key

- **WHEN** the model's page sets `source_sha: deadbeef` and a custom key `owner: team-a`
- **THEN** the written page has the plan's `source_sha` and still carries `owner: team-a` in its original position

#### Scenario: Model writes routed paths

- **WHEN** the model's page sets `routed_paths: [src/fake.go]`
- **THEN** the written page carries the routed set the tool stamped, or no `routed_paths` key when that set is unknown
