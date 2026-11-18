# GitHub access

`okf-operator` calls the GitHub REST API with a personal token for local use or as a GitHub App in production.

## Credentials

The first match wins:

1. GitHub App: `--app-id` and `--app-private-key-file`, or `OKF_GITHUB_APP_ID` and `OKF_GITHUB_APP_PRIVATE_KEY_FILE`. A flag overrides its variable.
1. `GITHUB_TOKEN`.

With neither, a command that calls GitHub exits 2 and names both options. Setting only one of the two App settings also exits 2.

`GITHUB_API_URL` overrides the API base URL (default `https://api.github.com/`). GitHub Actions sets it.

## Personal token

Any token that can read the source repo and the knowledge repo works. A fine-grained token needs read-only **Contents** and **Metadata** on both repos.

```bash
export GITHUB_TOKEN=$(gh auth token)
```

## GitHub App

### Create the App

1. Create a GitHub App under the organisation (Settings, Developer settings, GitHub Apps). No webhook is needed for the CLI.
1. Grant the repository permissions below.
1. Generate a private key and store the downloaded PEM file as a secret.
1. Install the App on the source repos and the knowledge repo.

### Permissions

| Command | Repository permission | Access    | Repos                       |
| ------- | --------------------- | --------- | --------------------------- |
| `plan`  | Contents              | Read-only | source repo, knowledge repo |
| `plan`  | Metadata              | Read-only | source repo, knowledge repo |
| `write` | Contents              | Read-only | source repo                 |
| `write` | Metadata              | Read-only | source repo                 |
| `write` | Pull requests         | Read-only | source repo                 |

Grant nothing else. Later commands document what they add.

### Tokens

For each repository owner a command touches, `okf-operator`:

1. signs an App JWT (RS256, 9-minute expiry) with the private key
1. looks up the installation with `GET /repos/{owner}/{repo}/installation`
1. mints an installation token with `POST /app/installations/{id}/access_tokens`, limited to that owner's repos the command uses and to the permissions in the table

Tokens live in memory for one run and are never written to disk. Even when the installation covers the whole organisation, each token only reaches the repos of that run.

The private key, the App JWT, the installation tokens and `GITHUB_TOKEN` are redacted from every log line at every log level, and never set as span attributes.
