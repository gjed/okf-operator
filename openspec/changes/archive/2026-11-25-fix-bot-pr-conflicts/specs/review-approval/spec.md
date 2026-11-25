# Spec Delta

## MODIFIED Requirements

### Requirement: Complete report only

`approve` SHALL post nothing unless the report contains every deterministic gate rule: `provenance`, `routed_set`, `allowlist`, `change_shape`, `size`, `links` and `mergeable`. A report missing one, or repeating any rule, SHALL yield `reason: "report_incomplete"`, so removing a failing rule from a report cannot produce an approval.

#### Scenario: Rule removed from the report

- **WHEN** a report passes but has no `size` rule
- **THEN** nothing is posted and the outcome has `reason: "report_incomplete"`

#### Scenario: Mergeable rule removed from the report

- **WHEN** a report passes but has no `mergeable` rule
- **THEN** nothing is posted and the outcome has `reason: "report_incomplete"`
