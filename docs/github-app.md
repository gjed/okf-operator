# GitHub access

`okf-operator` calls the GitHub REST API with a personal token for local use or as a GitHub App in production.

## Credentials

The first match wins:

1. GitHub App: `--app-id` and `--app-private-key-file`, or `OKF_GITHUB_APP_ID` and `OKF_GITHUB_APP_PRIVATE_KEY_FILE`. A flag overrides its variable.
1. `GITHUB_TOKEN`.

With neither, a command that calls GitHub exits 2 and names both options. Setting only one of the two App settings also exits 2.

`GITHUB_API_URL` overrides the API base URL (default `https://api.github.com/`). GitHub Actions sets it.

## Personal token

Any token that can read the source repo and the knowledge repo works for `plan` and `write`. A fine-grained token needs read-only **Contents** and **Metadata** on both repos, plus read-only **Pull requests** on the source repo. `propose` also needs read and write **Contents** and **Pull requests** on the knowledge repo. `gate` and `review` need read-only **Pull requests** on the knowledge repo.

`approve` never uses `GITHUB_TOKEN`. See [Review App](#review-app).

```bash
export GITHUB_TOKEN=$(gh auth token)
```

## GitHub App

### Create the App

1. Create a GitHub App under the organisation (Settings, Developer settings, GitHub Apps). No webhook is needed for the CLI; for the [controller](controller.md#webhooks), subscribe to **Push** and **Pull requests**.
1. Grant the repository permissions below.
1. Generate a private key and store the downloaded PEM file as a secret.
1. Install the App on the source repos and the knowledge repo.

### Permissions

| Command   | Repository permission | Access         | Repos                       |
| --------- | --------------------- | -------------- | --------------------------- |
| `plan`    | Contents              | Read-only      | source repo, knowledge repo |
| `plan`    | Metadata              | Read-only      | source repo, knowledge repo |
| `write`   | Contents              | Read-only      | source repo                 |
| `write`   | Metadata              | Read-only      | source repo                 |
| `write`   | Pull requests         | Read-only      | source repo                 |
| `propose` | Contents              | Read and write | knowledge repo              |
| `propose` | Pull requests         | Read and write | knowledge repo              |
| `propose` | Metadata              | Read-only      | knowledge repo              |
| `propose` | Contents              | Read-only      | source repo                 |
| `propose` | Pull requests         | Read-only      | source repo                 |
| `propose` | Metadata              | Read-only      | source repo                 |
| `gate`    | Contents              | Read-only      | source repo, knowledge repo |
| `gate`    | Metadata              | Read-only      | source repo, knowledge repo |
| `gate`    | Pull requests         | Read-only      | knowledge repo              |
| `review`  | Contents              | Read-only      | source repo, knowledge repo |
| `review`  | Metadata              | Read-only      | source repo, knowledge repo |
| `review`  | Pull requests         | Read-only      | knowledge repo              |

`propose` mints one token per repo, even when both repos have the same owner, so the source repo never gets write access.

Grant nothing else. Later commands document what they add.

### Tokens

For each repository owner a command touches, `okf-operator`:

1. signs an App JWT (RS256, 9-minute expiry) with the private key
1. looks up the installation with `GET /repos/{owner}/{repo}/installation`
1. mints an installation token with `POST /app/installations/{id}/access_tokens`, limited to that owner's repos the command uses and to the permissions in the table

`gate` and `review` mint the knowledge repo token and the source repo token separately, so the pull requests permission never reaches the source repo, even when both repos share an owner.

Tokens live in memory for one run and are never written to disk. Even when the installation covers the whole organisation, each token only reaches the repos of that run.

The private key, the App JWT, the installation tokens and `GITHUB_TOKEN` are redacted from every log line at every log level, and never set as span attributes.

## Review App

GitHub does not let a pull request's author approve it. `propose` opens knowledge PRs as the App above, so the approval that branch protection counts comes from a second, review-only App. Only `approve` uses it.

### Create the review App

[`deploy/review-app-manifest.json`](../deploy/review-app-manifest.json) is a [GitHub App manifest](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest) with the permissions below, no webhook and no events. App names are global on GitHub, so change `name` first. Then either register it through the manifest flow, or create the App by hand with the same settings:

| Repository permission | Access         |
| --------------------- | -------------- |
| Pull requests         | Read and write |
| Contents              | Read-only      |
| Metadata              | Read-only      |

Generate a private key, store it as a secret apart from the operator App's key, and install the review App on the knowledge repo only.

### Dismiss stale approvals

The knowledge repo's branch protection must have **Dismiss stale pull request approvals when new commits are pushed** enabled (in a ruleset: `dismiss_stale_reviews_on_push`). `propose` pushes new commits to an open knowledge PR, and without this setting an approval of an earlier head keeps counting for a head no gate or review has seen. `approve` pins its review to the reviewed commit and checks the head only when it posts.

### Review credentials

`approve` reads its own settings and nothing else:

1. `--review-app-id` and `--review-app-private-key-file`, or `OKF_REVIEW_GITHUB_APP_ID` and `OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE`.
1. `OKF_REVIEW_GITHUB_TOKEN`, a token of a user who did not open the PR, for local use.

It never falls back to `OKF_GITHUB_APP_ID`, `OKF_GITHUB_APP_PRIVATE_KEY_FILE` or `GITHUB_TOKEN`, and no other command reads the review settings. With none set, `approve` exits 2.

`approve` mints one token for the knowledge repo with **Pull requests** write and **Contents** and **Metadata** read. Its key, JWT and token are redacted like the operator App's.
