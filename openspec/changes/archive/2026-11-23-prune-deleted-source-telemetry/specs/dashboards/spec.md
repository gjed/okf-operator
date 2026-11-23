# Spec Delta

## ADDED Requirements

### Requirement: Deleted Source does not keep the stale-cursor alert firing

A deleted `Source` SHALL NOT keep `OkfOperatorCursorStale` firing: the controller stops reporting its staleness, and the README SHALL NOT tell users to restart the controller or silence the alert after deleting a `Source`.

#### Scenario: Deleted Source

- **WHEN** a `Source` whose cursor staleness fired `OkfOperatorCursorStale` is deleted
- **THEN** the controller stops reporting its staleness and the alert resolves without a controller restart
