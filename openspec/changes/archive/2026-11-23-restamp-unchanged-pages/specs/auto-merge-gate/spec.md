# Spec Delta

## ADDED Requirements

### Requirement: Frontmatter-only pages

A modified page whose change is only in its provenance keys, as a re-stamp makes it, SHALL be treated as any modified page: the `provenance`, `routed_set`, `allowlist`, `change_shape`, `size` and `links` rules SHALL apply to it unchanged, and it SHALL pass them when its `source_sha` is the plan's `to`. `gate` SHALL NOT add a rule or an exception for it. A page that is added is still not a modified page, so created pages stay human-only.

#### Scenario: Re-stamped page at the plan's to

- **WHEN** the PR modifies one planned page whose head differs from its base only in `source_sha` and `updated_at`, and `source_sha` is the plan's `to`
- **THEN** every deterministic rule passes

#### Scenario: Re-stamped page at another commit

- **WHEN** the same page has a `source_sha` that exists in `source_repo` but is not the plan's `to`
- **THEN** the `provenance` rule fails
