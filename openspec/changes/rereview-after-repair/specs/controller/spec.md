# Spec Delta

## MODIFIED Requirements

### Requirement: Dismiss stale approvals

A run that pushes nothing SHALL gate the bot PR head again, without a review, when that head is the one the controller last pushed, it still holds the plan behind it and the run's repair did not move the head (see "Repaired head is reviewed and approved again"). When a gate run fails on head H, the controller SHALL dismiss, with the review App, every approving review the review App posted on commit H.

#### Scenario: Gate fails on an approved head

- **WHEN** the review App approved head H, and a later run that repairs nothing and pushes nothing gates H again and the gate fails
- **THEN** that approval is dismissed through `PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, and nothing is reviewed or approved

### Requirement: Approvals of superseded heads are dismissed

On every run with an open bot PR, once the PR head is settled (after update-branch or after the regeneration commit) and before the checkout of that head, the controller SHALL dismiss, with the review App, every approving review the review App posted on a commit other than that head. The message SHALL name the commit. Reviews by other users and the review App's approvals of the head SHALL be left alone.

#### Scenario: Behind PR updated

- **WHEN** the review App approved head H and the run updates the branch off H
- **THEN** that approval is dismissed through `PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals` and a human's approval of H stays

#### Scenario: Conflicting PR regenerated

- **WHEN** the review App approved head H and the run pushes the regeneration commit
- **THEN** that approval is dismissed

#### Scenario: Approval of the current head

- **WHEN** the review App approved the head the run builds on
- **THEN** that approval is not dismissed

## ADDED Requirements

### Requirement: Repaired head is reviewed and approved again

When a run's repair moved the bot PR head, by update-branch or by the regeneration commit, and `propose` pushes nothing, the controller SHALL run the gate, review and approval on the repaired head as it does after a push, with the plan remembered for the head, instead of only gating it again. The review SHALL cover the plan of the PR's whole range. When `propose` pushes new commits the run SHALL review and approve once.

#### Scenario: Behind PR updated, nothing new to propose

- **WHEN** the run updates the branch of a bot PR whose plan is remembered and `propose` pushes nothing
- **THEN** every page of the plan is reviewed once, the gate passes with the review and the review App approves the new head

#### Scenario: Conflicting PR regenerated, nothing new to propose

- **WHEN** the run regenerates the conflicting page of a PR whose range covers two pages and `propose` pushes nothing
- **THEN** both pages are reviewed against the plan of the whole range and the review App approves the regeneration commit

#### Scenario: Repaired head fails the gate

- **WHEN** the gate without a review fails on the repaired head and the review App approved that head
- **THEN** that approval is dismissed and no reviewer model call is made

#### Scenario: Repair and new commits

- **WHEN** the run's repair moved the head and `propose` pushes new commits
- **THEN** the pushed head is reviewed and approved once

### Requirement: Other heads are not reviewed again

A run that moved no head and pushes nothing SHALL only gate the head again, without a review, when the controller holds the plan behind it. A reconcile PR SHALL NOT be reviewed or approved, repaired or not. A repaired head whose plan the controller does not hold SHALL NOT be gated, reviewed or approved.

#### Scenario: No repair and no new commits

- **WHEN** a run repairs nothing, pushes nothing and the plan behind the head is remembered
- **THEN** the head is gated without a review, no review call is made and nothing is approved

#### Scenario: Reconcile PR

- **WHEN** a run updates the branch of a reconcile PR
- **THEN** nothing is gated, reviewed or approved

#### Scenario: Plan lost on restart

- **WHEN** the controller restarted after the PR head was pushed and a run updates its branch
- **THEN** the head is not gated, reviewed or approved
