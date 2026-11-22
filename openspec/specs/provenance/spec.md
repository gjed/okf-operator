# provenance Specification

## Purpose

Defines the provenance frontmatter that every generated page carries and the `internal/provenance` API that `write` stamps it with and `gate` reads it with.

## Requirements

### Requirement: Provenance fields

A generated page SHALL carry five top-level frontmatter keys: `source_repo` (`owner/name`), `source_sha` (the full 40-character commit SHA the page reflects, the plan's `to`), `source_paths` (sorted list of the source paths that routed to the page), `updated_at` (RFC 3339 UTC timestamp) and `generated_by` (`okf-operator/<version>/<model>`).

#### Scenario: Stamped page

- **WHEN** `write` changes a page for source `acme/api` at `to` SHA `abc...` from path `api/handler.go` with model `m1` on binary version `1.2.3`
- **THEN** the page frontmatter has `source_repo: acme/api`, that `source_sha`, `source_paths: [api/handler.go]`, an `updated_at` in UTC and `generated_by: okf-operator/1.2.3/m1`

### Requirement: Tool-owned provenance

Provenance SHALL be stamped by the tool after the model returns. Any provenance key in the model's output SHALL be overwritten, and a `routed_paths` key SHALL be removed when the tool stamps an unknown routed set. All other frontmatter keys, their order and the body SHALL be preserved.

#### Scenario: Model writes a provenance key

- **WHEN** the model's page sets `source_sha: deadbeef` and a custom key `owner: team-a`
- **THEN** the written page has the plan's `source_sha` and still carries `owner: team-a` in its original position

#### Scenario: Model writes routed paths

- **WHEN** the model's page sets `routed_paths: [src/fake.go]`
- **THEN** the written page carries the routed set the tool stamped, or no `routed_paths` key when that set is unknown

### Requirement: Parse and Stamp API

`internal/provenance` SHALL expose `Fields`, `Parse(page []byte) (Fields, []byte, error)` and `Stamp(page []byte, f Fields) ([]byte, error)`. `Parse` SHALL return the provenance fields (zero values for absent keys) and the body after the frontmatter. Both SHALL return an error for a page without a YAML frontmatter mapping or with a provenance key of the wrong type. `Stamp` followed by `Parse` SHALL return the stamped fields and the original body byte for byte.

#### Scenario: Page without frontmatter

- **WHEN** `Parse` receives a page that does not start with a `---` frontmatter block
- **THEN** it returns an error

#### Scenario: Round trip

- **WHEN** a page is stamped with fields F and then parsed
- **THEN** `Parse` returns F and the body is byte-identical to the input body

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
