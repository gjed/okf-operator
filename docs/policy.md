# Auto-merge policy

The auto-merge policy tells `gate` which knowledge PRs may merge without a human: which concept types and bundles are allowed, and how large a generated change may be.

`gate` reads it from `.kb/policy.yaml` on the PR's base branch, never from the PR head, so a PR cannot change the policy it is judged by. A PR that touches anything under `.kb/` fails the change-shape rule.

## Example

```yaml
version: 1
auto_merge:
  types: [Service]
  bundles: [product]
limits:
  page_lines: 200
  pr_lines: 600
```

A complete file is in [`examples/policy.yaml`](../examples/policy.yaml).

## Schema

| Key                  | Required | Description                                                                           |
| -------------------- | -------- | ------------------------------------------------------------------------------------- |
| `version`            | yes      | Must be `1`. Any other value fails loading.                                           |
| `auto_merge.types`   | no       | Concept types (the page frontmatter `type`) allowed to auto-merge. Empty allows none. |
| `auto_merge.bundles` | no       | Bundle directories allowed to auto-merge. Empty allows none.                          |
| `limits.page_lines`  | yes      | Max changed lines (added plus removed) in one page.                                   |
| `limits.pr_lines`    | yes      | Max changed lines (added plus removed) across the PR.                                 |

A page passes the allowlist only when both its type and its bundle are listed. With `auto_merge` omitted, nothing auto-merges.

Unknown keys fail loading, so a typo cannot silently widen or narrow the allowlist.

## Validation

The file is validated when it is loaded, and every problem is reported in one error. Loading fails when:

- `version` is not `1`
- a key is unknown
- a type or bundle entry is empty or appears twice
- a bundle entry is absolute, contains `..` or is not a clean path
- `limits.page_lines` or `limits.pr_lines` is missing, zero or negative

A missing or invalid policy makes `gate` exit 1 without a report.
