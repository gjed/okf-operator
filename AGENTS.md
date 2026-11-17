# okf-operator Agent Rules

## Commit Convention

All commits use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format:

```text
<type>(<scope>): <description>
```

See the `conventional-commits` skill for the full type list and rules.

## OpenSpec Workflow

When applying an OpenSpec change (`openspec-apply-change` skill or `/opsx-apply`):

1. Fetch and pull the default branch to ensure it is up to date.

1. Create a git worktree under `.worktrees/` — flat, no subfolders:

   ```bash
   git worktree add .worktrees/<change-name> -b <change-name> origin/main
   ```

1. Do all implementation work inside that worktree.

1. Commit using Conventional Commits as work progresses.

1. Archive the change with `openspec archive` in the same PR that adds it, unless the user says otherwise.

### Worktree Layout

```text
.worktrees/
  <change-name>/   ← one flat directory per change, no nesting
```

Never nest worktrees (e.g., `.worktrees/foo/bar/` is forbidden).

Never clone a repository to get a second working copy. Use a worktree inside the main clone.
