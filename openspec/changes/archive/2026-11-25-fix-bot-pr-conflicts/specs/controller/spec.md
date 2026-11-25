# Spec Delta

## ADDED Requirements

### Requirement: Bot PR repair

A run SHALL read the merge state of the open bot PR before the checkout and repair the PR when it is `behind` (update-branch) or `dirty` (regeneration of the conflicting pages). The repair SHALL use the writer App's knowledge-repo token. It SHALL fail the run, with the stage `checkout` for the merge-state read and update-branch and the stage `repair` for a regeneration, when it cannot complete.

#### Scenario: Update-branch fails

- **WHEN** update-branch answers 422
- **THEN** the run fails at the stage `checkout` and nothing else is written

#### Scenario: Regeneration refused

- **WHEN** the repair refuses a PR whose body omits its earlier ranges
- **THEN** the run fails at the stage `repair` and the cursor is unchanged

## MODIFIED Requirements

### Requirement: Run order

A run SHALL repair the open bot PR, if any, then materialise the knowledge checkout from the GitHub tarball API at the head of the open bot PR as repaired, else of the default branch, load the routing map from the default branch, and call in order `plan.Build` (or `reconcile.Build` when the reconcile interval is due), `write.Run` and `propose.Run`.

#### Scenario: Open bot PR

- **WHEN** a clean bot PR for the source is open
- **THEN** the checkout is its head and `propose` gets that head as the knowledge base

#### Scenario: Behind bot PR

- **WHEN** the bot PR is `behind`
- **THEN** the controller updates the branch, and the checkout and `propose`'s knowledge base are the new head

#### Scenario: Conflicting bot PR

- **WHEN** the bot PR is `dirty`
- **THEN** the controller pushes the two-parent commit that regenerates the conflicting pages, and the checkout and `propose`'s knowledge base are that commit

#### Scenario: Weekly reconcile

- **WHEN** a `Source`'s reconcile interval has passed since its last reconcile
- **THEN** the run calls `reconcile.Build`, not `plan.Build`, records `lastReconcileTime`, and stops after `propose`
