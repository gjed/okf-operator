# Spec Delta

## ADDED Requirements

### Requirement: Routed paths from a reconcile entry

For a page whose version 2 `reconcile.pages` entry has `routed_paths`, `write` SHALL stamp that list as the page's `routed_paths`.

#### Scenario: Widened page written

- **WHEN** a version 2 page with reason `widened`, `source_paths: [src/b.go]` and entry `routed_paths: [src/a.go, src/b.go]` is changed
- **THEN** the written page has `routed_paths: [src/a.go, src/b.go]`

### Requirement: Routed paths carried forward

For any other page, `write` SHALL stamp as `routed_paths` the union of the current page's `routed_paths` and the page's `source_paths`, minus each path the plan's `changed_paths` report as `removed` and each `previous_path` of a `renamed` entry. A current page without `routed_paths` SHALL be stamped without it, and a created page SHALL record its `source_paths` minus those paths.

#### Scenario: Version 1 update of a page with routed paths

- **WHEN** a page records `routed_paths: [src/a.go, src/old.go]` and a version 1 plan renames `src/old.go` to `src/new.go`, with `source_paths: [src/new.go, src/old.go]`
- **THEN** the written page has `routed_paths: [src/a.go, src/new.go]`

#### Scenario: Version 1 update of a page without routed paths

- **WHEN** a page without `routed_paths` is changed from a version 1 plan
- **THEN** the written page has no `routed_paths`

#### Scenario: Version 1 create

- **WHEN** a version 1 plan creates a page from `source_paths: [docs/a.md]`
- **THEN** the created page has `routed_paths: [docs/a.md]`

## MODIFIED Requirements

### Requirement: Reconcile plan validation

A version 1 plan with a `reconcile` object, a version 2 plan without one, or a version 2 plan whose `reconcile.pages` do not match its pages one to one, name an unknown reason or evidence, give a `hunks` entry no full commit SHA `base`, give a `files` entry a `base`, or give `routed_paths` that are not sorted, unique and non-empty strings, SHALL be a runtime error before any GitHub or model call. An entry without `routed_paths` SHALL be accepted.

#### Scenario: Reconcile plan without page entries

- **WHEN** a version 2 plan has a page with no `reconcile.pages` entry
- **THEN** `write` exits 1 before any GitHub or model call

#### Scenario: Unsorted routed paths

- **WHEN** a version 2 entry has `routed_paths: [src/b.go, src/a.go]`
- **THEN** `write` exits 1 before any GitHub or model call
