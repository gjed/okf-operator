# Spec Delta

## Purpose

Defines the routing map: which source repositories feed the knowledge repo, which of their paths matter, and which knowledge pages each path maps to. It is the boundary that decides what the model may ever be asked to change.

## ADDED Requirements

### Requirement: Routing map location and version

The routing map SHALL be read from `.kb/routing.yaml` in the knowledge repo checkout unless a path is given explicitly. It SHALL declare `version: 1`. Any other version SHALL fail loading.

#### Scenario: Default location

- **WHEN** `plan` runs with `--knowledge-dir ./kb` and no `--routing` flag
- **THEN** the routing map is read from `./kb/.kb/routing.yaml`

#### Scenario: Unsupported version

- **WHEN** the routing map declares `version: 2`
- **THEN** loading fails with an error naming the supported version, and the command exits 1

### Requirement: Source binding to one bundle

Each source entry SHALL name one source repo as `owner/name` and exactly one bundle. A source repo SHALL appear at most once in the map.

#### Scenario: Duplicate source

- **WHEN** two source entries name the same repo
- **THEN** loading fails and the error names the repo

### Requirement: Routes stay inside the bundle

Every route target page SHALL be a relative `.md` path inside its source's bundle directory. A target that escapes the bundle (absolute path, `..` segment, or another bundle's directory) SHALL fail loading.

#### Scenario: Target outside bundle

- **WHEN** a source bound to bundle `product` routes to `internal-infra/runbooks/x.md`
- **THEN** loading fails and the error names the source and the target

#### Scenario: Path traversal

- **WHEN** a route target is `product/../internal-infra/x.md`
- **THEN** loading fails

### Requirement: Path matching

A route SHALL match a changed path when the path matches any of the route's glob patterns, with `**` matching any number of path segments. Patterns are matched against repo-relative paths with `/` separators and are case-sensitive.

#### Scenario: Recursive glob

- **WHEN** a route has pattern `deploy/**/*.yaml` and `deploy/prod/app/values.yaml` changed
- **THEN** the route matches

#### Scenario: No match

- **WHEN** no route of the source matches a changed path
- **THEN** the path is reported as unrouted and no page is planned for it

### Requirement: Exclusions win over routes

A source MAY declare `exclude` globs. A changed path that matches any exclude glob SHALL NOT be routed, even if a route also matches it, and SHALL be reported as excluded, not as unrouted.

#### Scenario: Secret path excluded

- **WHEN** `exclude` contains `**/secrets/**` and `deploy/secrets/token.yaml` changed and a route matches `deploy/**`
- **THEN** the path is reported as excluded and routes to no page

### Requirement: One path may route to several pages

When a changed path matches several routes of its source, it SHALL be routed to every matched page. Each page SHALL appear once in the plan, listing all the changed paths that routed to it.

#### Scenario: Fan-out

- **WHEN** `api/openapi.yaml` matches a route to `product/services/api.md` and a route to `product/services/gateway.md`
- **THEN** both pages are planned and each lists `api/openapi.yaml` among its source paths

### Requirement: Page creation is opt-in per route

A route SHALL allow creating its target page only when it declares a concept `type`. The plan SHALL mark a target that does not exist in the knowledge checkout as `create` if the route declares a type, and as `missing` otherwise.

#### Scenario: Missing page without type

- **WHEN** a route without `type` targets a page that does not exist in the checkout
- **THEN** the planned page has state `missing` and later stages will not create it

#### Scenario: Missing page with type

- **WHEN** a route with `type: Service` targets a page that does not exist
- **THEN** the planned page has state `create` and carries type `Service`
