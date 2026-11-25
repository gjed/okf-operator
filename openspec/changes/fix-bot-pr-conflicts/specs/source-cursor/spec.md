# Spec Delta

## ADDED Requirements

### Requirement: Repairs never move the cursor

Updating a behind bot PR and regenerating a conflicting one SHALL NOT read or write the cursor, and the range a regeneration covers SHALL NOT be written back as a cursor. The cursor SHALL only move through `propose`, from the plan's `from` to its `to`, so it never moves backwards.

#### Scenario: Regeneration run

- **WHEN** a run regenerates a conflicting bot PR whose body records the range `A..B` and the cursor holds `B`
- **THEN** the cursor still holds `B` after the repair, and after the run it holds the new source HEAD or `B`
