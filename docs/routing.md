# Routing map

The routing map tells `okf-operator` which source repositories feed a knowledge repo, which of their paths matter, and which knowledge pages each path maps to. A changed path that routes nowhere never reaches a later stage.

`plan` reads it from `.kb/routing.yaml` in the knowledge repo checkout given with `--knowledge-dir`. `--routing <file>` reads it from another path.

## Example

```yaml
version: 1
sources:
  - repo: acme/api
    bundle: product
    exclude: ["**/secrets/**", "**/*.pem"]
    routes:
      - page: product/services/api.md
        paths: ["api/**", "cmd/server/**"]
      - page: product/services/billing.md
        type: Service
        paths: ["billing/**"]
```

A complete file is in [`examples/routing.yaml`](../examples/routing.yaml).

## Schema

| Key                        | Required | Description                                                                          |
| -------------------------- | -------- | ------------------------------------------------------------------------------------ |
| `version`                  | yes      | Must be `1`. Any other value fails loading.                                          |
| `sources[].repo`           | yes      | Source repo as `owner/name`. Each repo appears at most once, compared ignoring case. |
| `sources[].bundle`         | yes      | Bundle directory in the knowledge repo that this source writes to.                   |
| `sources[].exclude`        | no       | Globs for paths that never route, even when a route matches them.                    |
| `sources[].routes[].page`  | yes      | Target page: a relative `.md` path inside the bundle directory.                      |
| `sources[].routes[].type`  | no       | Concept type. When set, the page may be created if it does not exist.                |
| `sources[].routes[].paths` | yes      | Globs for source paths that route to the page. At least one.                         |

Unknown keys fail loading, so a typo cannot silently disable a route.

## Validation

The file is validated when it is loaded, and every problem is reported in one error. Loading fails when:

- `version` is not `1`
- a key is unknown
- a repo is not `owner/name`, or appears twice
- a bundle is empty, absolute or contains `..`
- a target page is absolute, contains a `..` segment, is not a clean path (`./`, `//`), does not end in `.md`, or is outside its source's bundle
- two routes of one source give the same page different `type` values
- a route has no paths
- a glob in `paths` or `exclude` is invalid

A routing map that fails to load makes `plan` exit 1.

## Matching

Globs use [doublestar](https://github.com/bmatcuk/doublestar) syntax and match repo-relative paths with `/` separators. Matching is case-sensitive.

- `*` matches within one path segment, `**` matches any number of segments: `deploy/**/*.yaml` matches `deploy/prod/app/values.yaml`.
- A path that matches an `exclude` glob is reported as excluded and routes nowhere.
- A path that matches no route is reported as unrouted and logged once at info level.
- A path that matches several routes routes to every matched page. Each page appears once in the plan with all the paths that routed to it.
- A renamed file routes by both its previous and its new path.

## Page state

`plan` checks each target page in the knowledge checkout:

| Page exists | Route has `type` | State     |
| ----------- | ---------------- | --------- |
| yes         | either           | `update`  |
| no          | yes              | `create`  |
| no          | no               | `missing` |

Later stages never create a `missing` page.

## Sources not in the map

`plan` for a source repo with no entry succeeds with an empty plan and logs `source not onboarded`.
