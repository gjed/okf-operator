# Operator state

`okf-operator` keeps its state in the knowledge repo, on a branch named `kb-state`. The branch holds no pages and is never merged into the default branch.

## Layout

```text
kb-state
└── cursors/
    └── <owner>/
        └── <name>     # cursor for source repo <owner>/<name>
```

## Cursors

A cursor is the last source commit the operator processed for one source repo. Every run diffs from the cursor to the source default branch HEAD, never from the event that triggered it, so duplicate and out-of-order webhooks are harmless.

- Path: `cursors/<owner>/<name>`, no extension. GitHub owner and repo names cannot contain `/`, so the path is unambiguous.
- Content: one full 40-character lowercase hex commit SHA followed by a newline.
- One file per source, so updating one source never rewrites another, and `git log cursors/<owner>/<name>` is that source's processing history.

```bash
gh api 'repos/acme/kb/contents/cursors/acme/api?ref=kb-state' --jq .content | base64 -d
```

`plan` only reads cursors. `propose` writes and advances them.

## How plan treats the cursor

| Cursor                                      | Result                                                           |
| ------------------------------------------- | ---------------------------------------------------------------- |
| File or `kb-state` branch missing (404)     | Initial plan: `initial: true`, no pages, `to` is the source HEAD |
| Equal to the end commit                     | Empty plan: `from` equals `to`, no changed paths                 |
| Ancestor of the end commit                  | Normal plan of the range                                         |
| Not an ancestor (force-push, rewrite, gone) | Exit 1: cursor diverged, `reconcile` required                    |
| Malformed content                           | Exit 1: the error names the file                                 |
| Any other API error                         | Exit 1, so an outage never looks like a first run                |

A source with no cursor is never backfilled by `plan`. The first run only anchors the range end, and backfill is `reconcile`'s job.
