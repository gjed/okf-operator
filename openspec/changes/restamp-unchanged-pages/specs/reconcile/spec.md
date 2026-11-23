# Spec Delta

## MODIFIED Requirements

### Requirement: Current pages are left out

A page whose `source_sha` equals `to`, or whose routed paths did not change since its `source_sha`, SHALL NOT be planned unless it is planned as `widened`. A page that `write` re-stamped at `to`, with the `routed_paths` of its plan entry, is such a page: it SHALL NOT be planned again.

#### Scenario: Page already at the end commit

- **WHEN** a page's `source_sha` equals `to` and every path routed to it at `to` is in its `routed_paths`
- **THEN** the page is not planned

#### Scenario: Page re-stamped by write

- **WHEN** a page was planned `stale` or `widened`, the model answered `NO_CHANGE`, `write` re-stamped it, and `reconcile` runs again at the same `to`
- **THEN** the page is not planned
