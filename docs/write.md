# Writing pages

`write` takes a plan from `okf-operator plan` and asks a model for the updated content of each planned page, or for the content of a new page. It validates every reply, stamps provenance and writes the changed and created pages into the knowledge checkout. It never commits, pushes or opens a PR; that is `propose`.

## Model endpoint

`write` calls one OpenAI-compatible chat completions endpoint: `POST <base-url>/chat/completions`. Ollama, vLLM, LiteLLM and most hosted providers expose one.

| Setting  | Flag               | Environment          | Required |
| -------- | ------------------ | -------------------- | -------- |
| Base URL | `--model-base-url` | `OKF_MODEL_BASE_URL` | yes      |
| Model    | `--model`          | `OKF_MODEL`          | yes      |
| API key  | none               | `OKF_MODEL_API_KEY`  | no       |

The API key is read from the environment only, so it never shows in a process list. Without it, no `Authorization` header is sent. The key never appears in logs, spans, errors or the report. The model receives text and returns text. It never receives GitHub credentials, and its reply can only become the page it was asked about.

## What the model sees

One request per page with state `update`. The prompts are embedded in the binary from [`prompts/`](../prompts/), so a given version always sends the same instructions. Each request holds these blocks, in order, each between `<<<NAME>>>` and `<<<END-NAME>>>` marker lines:

1. The writing guidance from `.kb/profile.md`, if it exists.
1. The template for the page's `type` from `.kb/templates/<type>.md`, if it exists.
1. Up to two style-reference pages from the same bundle.
1. The current page.
1. The source data, in a block the prompt marks as untrusted: the titles and bodies of the merged PRs behind the range's commits (bodies capped at 4 KiB, at most 16 KiB of PR text per prompt, newest PRs first, with a note of how many older ones were left out), and the compare patches of the page's `source_paths` only. A reconcile plan sends other source data, see [Reconcile plans](#reconcile-plans).

The model replies `NO_CHANGE` or the complete updated page.

One request per page with state `create`, from a separate prompt pair. It holds the writing guidance, the template for the route's `type` (required), up to two reference pages of that `type`, and the same untrusted source data. The model replies with the complete new page.

A page with state `missing` (no page and no `type` on its route) is never sent to the model.

## Knowledge repo config

Both files are optional for updates.

- `.kb/profile.md`: free-form writing guidance for every page, such as audience, tone and what to leave out.
- `.kb/templates/<type>.md`: the skeleton for pages of one concept `type`. The file name is the `type` value from the page frontmatter, for example `.kb/templates/Service.md` for `type: Service`. A page with state `create` needs the template of its route's `type`; without it the page is dropped with reason `template_missing`.

Style references are picked automatically. The candidates are the `.md` files in the bundle that have a frontmatter `type`. `index.md`, `log.md`, `.kb/` and every page in the plan are excluded. Pages of the same `type` as the target come first, then the rest, each in path order. The first two are used. A create uses pages of its route's `type` only.

## Validation

Each reply must be a valid OKF concept page: frontmatter that is a YAML mapping, a non-empty string `type`, a non-empty body and a name that is not `index.md` or `log.md`. It must keep the current page's `type` and every other top-level frontmatter key, and must not contain a block marker line from the prompt. A reply for a create must be a complete page, not `NO_CHANGE`, whose `type` is the route's `type` and that keeps every top-level frontmatter key of the template. An invalid reply, or one cut off at the token limit, gets one retry with the error. A second failure drops the page.

## Provenance

`write` stamps these frontmatter keys on every changed and created page after the model replies, overwriting anything the model wrote under the same keys:

| Key            | Value                                            |
| -------------- | ------------------------------------------------ |
| `source_repo`  | Plan `source`, `owner/name`                      |
| `source_sha`   | Plan `to`, the full commit SHA the page reflects |
| `source_paths` | The page's routed source paths, sorted           |
| `updated_at`   | Time of the run, RFC 3339 UTC                    |
| `generated_by` | `okf-operator/<version>/<model>`                 |
| `routed_paths` | The source files the page reflects, sorted       |

`routed_paths` is what lets `reconcile` notice a route widened to unchanged files; see [Reconciling a source](reconcile.md#widened-routes). `write` sets it to the reconcile entry's `routed_paths` when the plan has one. Otherwise it takes the page's current `routed_paths`, adds the page's `source_paths` and drops the files the plan's `changed_paths` report as removed or renamed away. A created page records its `source_paths`. A page that has no `routed_paths` yet keeps none, and a model-written value is removed. The current page goes into the prompt without `routed_paths`, so a broad route does not count against `--max-tokens-per-page`.

Other frontmatter keys keep their order and values. In a `plan` run, a reply that matches the current page apart from these keys counts as unchanged and the file is not touched. A reconcile plan re-stamps such a page instead, see [Reconcile plans](#reconcile-plans).

## Budget

Both limits are checked before the first model call. A run over either one exits 1 and calls no model.

- `--max-pages` (default 10): the most `update` and `create` pages in one run.
- `--max-tokens-per-page` (default 16000): the most tokens for one page, all attempts together. The prompt is estimated at one token per three bytes, and the whole current page, or the template for a create, must fit as output. A retry that would not fit is not made and the page is dropped.

## Report

`write` prints one JSON document to stdout:

```json
{
  "version": 3,
  "source": "acme/api",
  "bundle": "product",
  "from": "1111111111111111111111111111111111111111",
  "to": "2222222222222222222222222222222222222222",
  "model": "m1",
  "generated_by": "okf-operator/1.2.3/m1",
  "pages": [
    {
      "path": "product/services/api.md",
      "result": "changed",
      "reason": "",
      "attempts": 1,
      "tokens": { "prompt": 900, "completion": 400 }
    },
    {
      "path": "product/services/billing.md",
      "result": "created",
      "reason": "",
      "attempts": 1,
      "tokens": { "prompt": 700, "completion": 300 }
    },
    {
      "path": "product/services/search.md",
      "result": "dropped",
      "reason": "patch_unavailable",
      "attempts": 0,
      "tokens": { "prompt": 0, "completion": 0 }
    }
  ],
  "tokens": { "prompt": 1600, "completion": 700 }
}
```

`version` is `3` since the `restamped` result was added; it was `2` after `created`. `propose` accepts only the version it was built with and fails clearly on another, so re-run `write` after upgrading both. `pages` lists every plan page, sorted by path. `result` is `changed`, `created`, `restamped`, `unchanged` or `dropped`. `reason` is empty unless the page was dropped:

| Reason              | Meaning                                                                  |
| ------------------- | ------------------------------------------------------------------------ |
| `patch_unavailable` | The compare API omitted the patch of a source path (binary or too large) |
| `validation_failed` | Two invalid replies, or no budget left for the retry                     |
| `template_missing`  | The page has state `create` and its route's `type` has no template       |
| `page_missing`      | The page has state `missing`                                             |
| `file_unavailable`  | A whole source file is missing, binary or over 64 KiB                    |

`tokens` uses the usage the endpoint reports, or the estimate when it reports none.

## Reconcile plans

`write` also takes the version 2 plans `reconcile` prints; see [Reconciling a source](reconcile.md). Each page carries its own evidence:

- `hunks`: the compare patches of the page's `base` to the plan's `to`, for its `source_paths`, with one compare call per distinct base. A missing patch drops the page with `patch_unavailable`, as for a `plan` run.
- `files`: the whole content at `to` of each of its `source_paths`, inside the untrusted block as `Full content of <path>:`. A file that is missing, not a regular file, not valid UTF-8, contains a NUL byte or is over 64 KiB drops the page with `file_unavailable` before any model call. A file is never sent in part.

Version 2 prompts carry no source PR context: each page has its own range, or none. Budget, validation, provenance (`source_sha` is the plan's `to`) and the working-tree rules are the same as for a `plan` run, so whole files count against `--max-tokens-per-page`.

### Pages the model leaves unchanged

When the model answers `NO_CHANGE` for an `update` page of a reconcile plan, or returns the page unchanged apart from provenance, `write` re-stamps it: the provenance values (`source_sha`, `source_paths`, `routed_paths`, `updated_at`, `generated_by`) are refreshed as for a changed page, and the body, the other frontmatter keys, their order and the comments stay as they were. The page is reported as `restamped` and counted in `okf.write.pages` with `okf.write.result=restamped`. Without this, a stale or widened page the model judges current would keep its old `source_sha` or `routed_paths`, and every later `reconcile` would plan it, and pay for a model call, again. A re-stamp that would leave the file byte for byte the same is reported `unchanged`.

Only reconcile plans re-stamp. A `plan` run leaves an unchanged page untouched, because its range is the cursor's and the next `plan` does not look at the page again.

## Working tree

Files change only when `write` exits 0, and then exactly the pages with result `changed` or `restamped` are modified and the pages with result `created` are added. No other file is created, and none is deleted. A create never overwrites a file: a plan whose create path already exists in the checkout fails before any model call (run `plan` against the same checkout as `write`, the open bot PR's branch when there is one, so a page created in that PR is planned as an update), and the new file is linked into place only if its name is still free. `write` does not add created pages to the bundle `index.md`; the reviewer of the knowledge PR does. A run that exits non-zero leaves the checkout untouched. The one exception is a write error such as a full disk after some pages were written; the error lists those pages.

`propose` takes the report and the working tree as its input.
