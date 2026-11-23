# Spec Delta

## ADDED Requirements

### Requirement: Deleted Source telemetry

When a reconcile finds that its `Source` no longer exists, the controller SHALL drop that `Source`'s pending merge time and cursor staleness entry, so `okf.controller.cursor.staleness` stops reporting it, and SHALL leave the entries of other `Source`s unchanged. A read error other than not found SHALL leave the entries unchanged.

#### Scenario: Deleted Source stops reporting staleness

- **WHEN** two `Source`s have reported `okf.controller.cursor.staleness`, one is deleted and the controller reconciles it
- **THEN** the next collection of the gauge has no series for the deleted `Source`'s repo and still has the series of the other `Source`
