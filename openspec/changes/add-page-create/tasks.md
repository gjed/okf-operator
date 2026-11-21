# Tasks

## 1. Write creates pages

- [x] 1.1 Add `prompts/create-system.md` and `prompts/create.md` (embedded in `prompts/embed.go`) and replace the `create_unsupported` drop in `internal/write` with a create path: template required (`template_missing` drop, no model call), same-type references, existing path → exit 1 before any GitHub or model call, creates count against `--max-pages`, reply `type` must equal the route `type` and keep the template's frontmatter keys, `NO_CHANGE` invalid, write through a temp file and a hard link that never overwrites, result `created`, report `version: 2`, `okf.write.result=created` and `okf.write.pages.created`; verify tests for: created page from its template with stamped provenance, no template → `template_missing` and no model call, wrong reply type → invalid, existing create path → exit 1 and no model call, creates against `--max-pages`, telemetry counts; update `docs/write.md` and verify the markdown lint passes

## 2. Propose commits created pages

- [ ] 2.1 Make `propose` accept report v2 only, accept the result `created`, verify created pages as new regular files absent from the base tree, commit them with mode `100644` and list them in the PR body; verify tests for: created page committed as a new `100644` path, unreported new file still rejected, created page that is a symlink → exit 1, created page already in the base → exit 1, report page outside the plan → exit 1, report v1 → exit 1
