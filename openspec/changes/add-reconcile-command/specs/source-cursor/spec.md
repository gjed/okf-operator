# Spec Delta

## ADDED Requirements

### Requirement: Reconcile re-anchors a diverged cursor

A diverged cursor SHALL be re-anchored only by `reconcile` followed by `write` and `propose`: the version 2 plan carries the diverged cursor as `from` and the reconciled commit as `to`, and `propose` moves the cursor from `from` to `to` with the same compare-and-swap as for any plan, after the bot PR is opened or updated. A run that fails before that point SHALL leave the cursor unchanged, and a cursor moved by another run after `reconcile` read it SHALL fail `propose`.

#### Scenario: Re-anchor after a force-push

- **WHEN** the source default branch was force-pushed, `plan` exits 1 with "reconcile required", and `reconcile`, `write` and `propose` then succeed
- **THEN** the cursor holds the reconciled `to`, and the next `plan` diffs from it

#### Scenario: Reconcile run fails

- **WHEN** `write` or `propose` fails during a reconcile run
- **THEN** the cursor still holds the diverged commit
