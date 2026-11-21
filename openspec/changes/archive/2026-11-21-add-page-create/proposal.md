# Proposal

## Why

`write` drops every plan page with `state: create` as `create_unsupported` ([#15](https://github.com/gjed/okf-operator/pull/15)). A new routed area in a source repo therefore produces no knowledge until someone writes the page by hand ([#16](https://github.com/gjed/okf-operator/issues/16)).

## What Changes

- `write` creates pages for `state: create`. The route's `type` needs a template at `.kb/templates/<type>.md`; without one the page is dropped with reason `template_missing` and no model call. The model receives the writing guidance, the template, up to two reference pages of the same `type`, the page's hunks and the untrusted PR block, from a separate embedded prompt pair (`prompts/create-system.md`, `prompts/create.md`).
- A create reply must be a complete valid page whose `type` equals the route `type` and that keeps the template's frontmatter keys. `NO_CHANGE` is invalid for a create. One retry, as for updates.
- A create whose path already exists under `--knowledge-dir` exits 1 before any model call. A created file is written through a temporary file and a hard link, so it never overwrites an existing file. Creates count against `--max-pages`.
- The write report gains the page result `created`, and its `version` becomes `2`. `propose` decodes report v2 only.
- `propose` commits created pages: each must be a new regular file absent from the base tree. Every other rule stays: unreported new files, symlinks and paths outside the plan fail. The PR body lists created pages.
- `write` does not add created pages to the bundle `index.md`; that is left to the human reviewer.
- `okf.write.pages` counts creates under `okf.write.result=created`, and the `write` span gains `okf.write.pages.created`.
- `gate` is unchanged: its change-shape rule allows updates only, so a PR with a created page always goes to a human.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `page-write`: page creation replaces "Page creation is out of scope"; budget, response contract, report, propose input contract and telemetry cover creates.
- `knowledge-proposal`: report v2, created pages verified as new files and committed, PR body lists them.

## Impact

- `internal/write`, `internal/propose`, `prompts/`, `docs/write.md`.
- The write report contract moves to version 2. A `propose` from an older release rejects a v2 report with a version error.
- No change to `internal/gate`, the `auto-merge-gate` spec, `routing` or `provenance`.
