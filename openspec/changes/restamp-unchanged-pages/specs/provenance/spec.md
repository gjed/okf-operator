# Spec Delta

## ADDED Requirements

### Requirement: Re-stamping a page keeps everything but the provenance values

Stamping the provenance of a page that the model did not change, as `write` does for an unchanged reconcile page, SHALL replace only the values of the provenance keys, append the missing ones in the order of `Stamp`, and keep the body, every other frontmatter key, their order and the comments byte for byte.

#### Scenario: Page with a comment and a custom key

- **WHEN** a page with a frontmatter comment, a custom key and a body with a code block is re-stamped
- **THEN** only the provenance values differ from the input; the comment, the custom key, the key order and the body are identical
