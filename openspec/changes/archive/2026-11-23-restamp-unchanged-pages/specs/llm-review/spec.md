# Spec Delta

## ADDED Requirements

### Requirement: Provenance-only pages pass without a model call

When the pages before and after are equal once their provenance is aligned (see "Provenance is not reviewed") and the page is neither new nor renamed and differs in its bytes, `review` SHALL pass the page without a model call or a source patch, with an empty `reason`, and log it at info level with the reason `provenance_only`. Any other change, a custom frontmatter key included, SHALL be reviewed as before. The result schema and its `version` SHALL NOT change.

#### Scenario: Only provenance changed

- **WHEN** the head changes only the page's `source_sha` and `updated_at`
- **THEN** the page has `pass: true` and an empty `reason`, the model receives no request for it, and no source compare is read for it

#### Scenario: Renamed page

- **WHEN** the PR renames a page without changing its content
- **THEN** the page is reviewed as before, not passed unreviewed

#### Scenario: Custom frontmatter key changed

- **WHEN** the head changes the provenance and a custom frontmatter key
- **THEN** the page is reviewed by the model
