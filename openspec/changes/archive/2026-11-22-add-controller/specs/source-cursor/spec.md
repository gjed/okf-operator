# Source Cursor

## ADDED Requirements

### Requirement: Declined ranges

When a bot PR from `okf/<owner>/<name>` is closed without merging, the controller SHALL record each source range of its body in the file `declined/<owner>/<name>` on `kb-state`, one line per range: the PR number, the range start (`-` when empty) and the range end, separated by single spaces. A PR SHALL be recorded once, with a compare-and-swap write. A declined range SHALL NOT block a later proposal and SHALL NOT move the cursor.

#### Scenario: Bot PR closed without merging

- **WHEN** bot PR 7 for `acme/api`, with one range `a..b`, is closed without merging
- **THEN** the next run of the `acme/api` `Source` appends `7 a b` to `declined/acme/api` on `kb-state`, and the cursor is unchanged

#### Scenario: Already recorded

- **WHEN** PR 7 is already in `declined/acme/api`
- **THEN** the run writes nothing to the file
