# Spec Delta

## ADDED Requirements

### Requirement: Approvals of superseded heads are dismissed

On every run with an open bot PR, once the PR head is settled (after update-branch or after the regeneration commit) and before the checkout of that head, the controller SHALL dismiss, with the review App, every approving review the review App posted on a commit other than that head. The message SHALL name the commit. The run SHALL NOT review or approve the head. Reviews by other users and the review App's approvals of the head SHALL be left alone.

#### Scenario: Behind PR updated

- **WHEN** the review App approved head H and the run updates the branch off H
- **THEN** that approval is dismissed through `PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, a human's approval of H stays, and nothing is reviewed or approved

#### Scenario: Conflicting PR regenerated

- **WHEN** the review App approved head H and the run pushes the regeneration commit
- **THEN** that approval is dismissed and no review is posted

#### Scenario: Approval of the current head

- **WHEN** the review App approved the head the run builds on
- **THEN** that approval is not dismissed

### Requirement: Dismissals are not lost

A failed dismissal SHALL fail the run at the stage `dismiss`, and a run that fails after it moved the bot PR head SHALL NOT leave the approvals of the old head in place: the dismissal SHALL come before the checkout of the moved head, and the next run SHALL dismiss what is left.

#### Scenario: A stage after the move fails

- **WHEN** the checkout of the moved head fails after update-branch
- **THEN** the approval of the old head was already dismissed

#### Scenario: Dismissal fails

- **WHEN** the dismissal fails after the head moved, and the next run finds the moved head as the PR head
- **THEN** the next run dismisses the approval of the old head without repairing again
