# Reconciling a source

`reconcile` plans every routed page of one source repo against the source tree at one commit, instead of the files changed since the cursor. Use it to:

- backfill a newly onboarded source, whose first `plan` only anchors the cursor;
- recover when `plan` exits 1 because the cursor diverged (force-push, history rewrite) or the range lists 300 or more files;
- refresh pages that fell behind: a source file was renamed, a route now covers files the page never saw, or an earlier run dropped the page.

It calls no model and writes nothing. Its output is a plan of `version: 2`, which `write` and `propose` take like a `plan` output.

## How pages are chosen

`reconcile` lists the source tree at `to` (`--to`, default the source default branch HEAD) with the recursive git trees API and routes every file through `.kb/routing.yaml`. A truncated listing exits 1, because a partial tree would make routed pages look deleted. The compare API is only used to refine a page that is already selected, so the 300-file limit of `plan` does not apply.

The pages considered are the route targets of the source and every `.md` page of the bundle whose provenance `source_repo` is the source.

| Page                                                                         | Planned as                                                                      |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| Routed files exist, page missing                                             | `create` (route has a `type`) or `missing`, reason `backfill`, evidence `files` |
| Routed files exist, page has no frontmatter, no `source_sha` or another repo | `update`, reason `backfill`, evidence `files`                                   |
| `source_sha` has diverged from `to`                                          | `update`, reason `backfill`, evidence `files`                                   |
| A routed file is not in `routed_paths` and unchanged since `source_sha`      | `update`, reason `widened`, evidence `files`                                    |
| `source_sha` is an ancestor and routed files changed since                   | `update`, reason `stale` (or `rename`), evidence `hunks` from `source_sha`      |
| As above, but the compare lists 300 or more files                            | `update`, reason `stale`, evidence `files`                                      |
| `source_sha` equals `to`, or no routed file changed since, and none is new   | Not planned                                                                     |
| No route matches a file at `to` and none of its `source_paths` exists        | Deletion candidate                                                              |

A page the model leaves unchanged is not planned again: `write` re-stamps its provenance to `to` and records its `routed_paths`, so the next `reconcile` finds it current. See [Writing pages](write.md#pages-the-model-leaves-unchanged). The re-stamp is a frontmatter-only change in the bot PR, listed apart under "Provenance refreshed, content unchanged". Like every reconcile PR, it goes to a human.

A cursor or a page `source_sha` that is ahead of `to` exits 1: `reconcile` never moves either back to an older commit, so pass a `--to` at or after the cursor.

Reason `rename` means the compare reports one of the page's `source_paths` as the previous name of a renamed file. A route target whose frontmatter is malformed exits 1; other malformed bundle pages are skipped with a log line.

### Widened routes

Provenance `source_paths` holds only the files of the last run, so it cannot tell that a route now covers more files. Provenance `routed_paths` records the source files the page reflects at its `source_sha`. When a file routed to the page at `to` is missing from `routed_paths` and did not change since `source_sha`, the page is planned with reason `widened` and evidence `files`, even when its `source_sha` equals `to`. Its `source_paths` are those new files plus the routed files that changed since `source_sha`; the files it already reflects and that did not change are not sent. `widened` takes precedence over `stale` and `rename`, except when a file the route covers was removed or renamed away since `source_sha`: only hunks show a deletion, so the page is planned as `stale` or `rename` and its entry's `routed_paths` leave out the new files, which the next run plans as `widened`. A file added since `source_sha` is a change, not a widening, so it gives `stale` with its patch.

Pages written before `routed_paths` existed have none. Their routed set is unknown, so they are never planned as `widened` and are chosen exactly as before. The next `write` of such a page from a reconcile plan records the routed set at `to`. A route widened before that write is therefore not detected; to force a refresh, remove the page's `source_sha` so that `reconcile` backfills it.

## Plan version 2

```json
{
  "version": 2,
  "source": "cli/cli",
  "bundle": "tools",
  "from": "",
  "to": "0536d5e5288458c696ccbeac0e68d87128300069",
  "initial": true,
  "changed_paths": [],
  "pages": [
    { "path": "tools/gh/api.md", "state": "update", "type": "", "source_paths": ["pkg/cmd/api/api.go"] }
  ],
  "unrouted": [],
  "excluded": [],
  "reconcile": {
    "cursor": "missing",
    "pages": [
      { "path": "tools/gh/api.md", "reason": "stale", "evidence": "hunks", "base": "f7fd1d490b7edbccc21e0349ad16f09b6af08637", "routed_paths": ["pkg/cmd/api/api.go", "pkg/cmd/api/http.go"] }
    ],
    "deletion_candidates": [
      { "path": "tools/gh/legacy.md", "source_sha": "3e6aeafb1b796b862389c1abe4deba5e60fa0b01", "source_paths": ["legacy/main.go"] }
    ]
  }
}
```

Every version 1 key keeps its meaning. `from` is the cursor `reconcile` read, empty when there is none, and `initial` is true exactly then. `changed_paths`, `unrouted` and `excluded` are always empty. The `reconcile` object holds:

| Key                   | Meaning                                                                                                          |
| --------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `cursor`              | `missing`, `head` (at `to`), `ancestor` (behind `to`) or `diverged` (diverged from `to`)                         |
| `pages`               | One entry per plan page: `reason` (`backfill`, `stale`, `rename`, `widened`), `evidence`, `base`, `routed_paths` |
| `deletion_candidates` | Pages whose sources are gone, with their `source_sha` and `source_paths`                                         |

`routed_paths` lists every source file routed to the page at `to`; `write` stamps it as the page's provenance `routed_paths`. Plans printed before it existed omit it and keep their digest, but a `write` older than this key rejects a plan that carries it.

`evidence: hunks` makes `write` send the compare patches of `base..to` for the page's `source_paths`. `evidence: files` makes it send the whole files at `to` instead; see [Writing pages](write.md#reconcile-plans). Version 2 prompts carry no source PR context.

`plan` keeps printing version 1. A `write` or `propose` older than `reconcile` rejects version 2 with a version error, and `gate` and `review` reject it on purpose: a reconcile PR always goes to a human.

## Deletion candidates

A deletion candidate is never deleted or moved. It is listed in the `reconcile` output, in the reconcile section of the bot PR body, and in a warning `propose` logs. When no page changed, `propose` opens no PR, so the output and the log are the only places it appears. Remove the page or fix its route by hand.

## Running it

Use the same checkout rule as `plan`: the open bot PR's branch for the source, else the default branch, with the default branch's routing map.

```bash
branch=$(gh pr list -R acme/kb --head okf/acme/api --json headRefName --jq '.[0].headRefName // empty')
git -C kb fetch origin && git -C kb checkout --detach "origin/${branch:-main}"
git -C kb show origin/main:.kb/routing.yaml > routing.yaml

okf-operator reconcile --source acme/api --knowledge-repo acme/kb --knowledge-dir ./kb --routing routing.yaml > plan.json
okf-operator write --plan plan.json --knowledge-dir ./kb --max-pages 50 > report.json
okf-operator propose --plan plan.json --report report.json \
  --knowledge-repo acme/kb --knowledge-dir ./kb --knowledge-base "$(git -C kb rev-parse HEAD)"
```

A backfill can plan many pages, and pages with evidence `files` carry whole source files, so raise `--max-pages` and `--max-tokens-per-page` as needed; a run over either limit exits 1 before any model call.

`propose` then commits the pages, opens or updates the bot PR, and moves the cursor from `from` to `to`; see [Operator state](state.md#re-anchoring).

## Telemetry

`reconcile` annotates the dispatcher's `reconcile` span with `okf.source.repo`, `okf.bundle`, `okf.reconcile.from`, `okf.reconcile.to`, `okf.reconcile.cursor`, `okf.reconcile.re_anchor`, `okf.reconcile.deletion_candidates` and `okf.reconcile.pages.<reason>`. It adds the counters `okf.reconcile.pages` (attribute `okf.reconcile.reason`), `okf.reconcile.deletion_candidates` and `okf.reconcile.re_anchors`, labelled by source repo and bundle. A re-anchor is counted when it is planned; `propose` reports whether the cursor moved.
