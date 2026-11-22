# Tasks

## 1. API and generated files

- [x] 1.1 Add `api/v1alpha1` with `KnowledgeBase` and `Source` (group `okf.dev`), a pinned `controller-gen` behind `make generate` writing deepcopy code and CRDs to `deploy/crds/`, and a CI step that fails when `make generate` leaves a diff; verify with `make generate && git diff --exit-code` and `go build ./...`

## 2. GitHub access for the controller

- [ ] 2.1 Let `internal/github` take an App private key from memory and resolve the App slug with `GET /app`; verify tests for an in-memory key minting a token and the slug lookup with the key redacted

## 3. Controller

- [ ] 3.1 Add `internal/controller`: webhook HMAC verification and receiver, the `Source` reconciler with per-knowledge-repo serialisation, reconcile scheduling and status, the in-process pipeline (tarball checkout, default-branch routing map, plan or reconcile, write, propose, deterministic gate, review, gate with review, approve), declined ranges on `kb-state`, stale approval dismissal, `okf.controller.*` metrics and the `controller run` span; verify with `go test -race ./internal/controller/...` covering: signed webhook enqueues exactly the matching Source, bad or missing signature returns 401 and enqueues nothing, knowledge repo and own bot events dropped, two Sources of one knowledge repo never run concurrently, periodic requeue runs with no webhook (envtest), failed run sets the error condition and leaves the cursor untouched, weekly reconcile runs reconcile and not plan, failed gate on an approved head dismisses the approval, HMAC unit tests, secrets absent from logs, events, status and spans
- [ ] 3.2 Register `okf-operator controller` in `internal/cli/commands.go` with its flags; verify `go test ./internal/cli/...` for help and a bad flag

## 4. Manifests, CI and docs

- [ ] 4.1 Add `deploy/` RBAC, Secrets, sample `KnowledgeBase`, `Source` and Deployment, a pinned `setup-envtest` in the Makefile and the CI test job; verify with `kubectl apply --dry-run=client -f deploy/` and the CI test job
- [ ] 4.2 Document the controller in `docs/controller.md` (CRDs, webhook events, run order, serialisation, declined ranges, dismissal, metrics), `docs/state.md` and the README; verify the markdown lint passes
