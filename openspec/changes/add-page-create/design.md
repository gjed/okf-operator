# Design

## Context

`plan` marks a routed page `create` when the page does not exist in the knowledge repo and its route declares a `type`. `write` already loads the templates (`.kb/templates/<type>.md`) and the reference pages of a bundle. `propose` verifies the checkout against the base tree and rejects any changed page that is not in the base commit. `gate` allows only modifications of existing pages.

## Goals / Non-Goals

**Goals:**

- Create the page for every `create` entry from its template, with the same budget, validation, provenance and all-or-nothing rules as updates.
- Let `propose` commit exactly those new files and nothing else.

**Non-Goals:**

- Linking the new page from the bundle `index.md`.
- Auto-merging created pages through `gate`.
- Creating pages for `state: missing` (no route `type`).

## Decisions

### Template required

A create needs `.kb/templates/<type>.md` for the route `type`. Without it the page is dropped with reason `template_missing` before any model call, and the run goes on. Rejected: creating from the reference pages alone (no stated structure, so the model invents one per page), and failing the whole run (one unconfigured type would block every other page of the bundle).

### Separate prompts

Creates use `prompts/create-system.md` and `prompts/create.md`. The update system prompt is built around `CURRENT-PAGE` and `NO_CHANGE`; one prompt with conditional rules would weaken both. The blocks and markers are the same, so neutralisation and echo detection are shared. References are restricted to pages of the route `type`, because they are the only style the new page should follow.

### Reply contract

The reply must be a valid OKF page, its `type` must equal the route `type`, and it must keep every top-level frontmatter key of the template (other than provenance keys), when the template has frontmatter. `NO_CHANGE` is invalid: the route says the page should exist. Rejected: reporting `NO_CHANGE` as `unchanged` for a create, which would hide a page that never got written.

### Report result `created`, report version 2

A created page is reported with the new result `created`. `propose` needs to know which pages are new to check them against the base tree: with `changed` for both, it could no longer reject a modified page that is absent from the base. An older `propose` would reject `created` as an unknown result anyway, so the report `version` moves to `2` and `DecodeReport` accepts only `2`. Rejected: keeping `version: 1` (old consumers would fail on the result value instead of the version), and accepting both versions in `propose` (write and propose ship in the same binary and run in the same job).

### Never overwrite

Before any GitHub or model call, a create path that exists under `--knowledge-dir` (any file type, including a symlink) fails the run: the plan no longer matches the checkout. In the write phase the page is written to a temporary file and hard-linked to its name, which fails if the name exists, then the temporary file is removed. Missing parent directories are created in the write phase only. Rejected: `rename`, which replaces an existing file.

### Budget

Creates count against `--max-pages` with updates. The output reserve for a create is the template size, in place of the current page size.

### Index links

`write` does not edit `index.md`. Editing it would write a file outside the plan's page set, which the routed-set rule forbids, and the right place for the link is an editorial choice. The PR lists created pages so the reviewer can add the link.

### Gate

`internal/gate` and the `auto-merge-gate` spec are unchanged. A PR with a created page fails the change-shape rule and goes to a human.

### Telemetry

No new metric. `okf.write.pages` gets the attribute value `okf.write.result=created`, and the span gets `okf.write.pages.created`.

## Risks / Trade-offs

- [A template with no frontmatter gives no key contract] → the reply still needs a valid frontmatter with the route `type`.
- [Hard links are unsupported on some filesystems] → the write fails with the error and the pages already written are listed, as for a full disk.
