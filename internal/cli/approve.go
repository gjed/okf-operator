package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/approve"
	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
)

// Review App credentials. Only approve reads them, and approve reads nothing
// else, so the PR author's identity can never approve its own PR.
const (
	EnvReviewAppID      = "OKF_REVIEW_GITHUB_APP_ID"
	EnvReviewAppKeyFile = "OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE"
	EnvReviewToken      = "OKF_REVIEW_GITHUB_TOKEN"
)

var reviewCredentials = github.CredentialNames{
	AppIDFlag:   "review-app-id",
	KeyFileFlag: "review-app-private-key-file",
	AppIDEnv:    EnvReviewAppID,
	KeyFileEnv:  EnvReviewAppKeyFile,
	TokenEnv:    EnvReviewToken,
}

var approveCommand = Command{
	Name:    "approve",
	Summary: "approve a knowledge PR with the review App when its gate report passes, review included",
	Run:     runApprove,
}

func runApprove(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator approve --knowledge-repo owner/name --pr <number> --report <file|-> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	kbRepo := fs.String("knowledge-repo", "", "knowledge repo `owner/name` the PR belongs to (required)")
	pr := fs.Int("pr", 0, "pull request `number` in the knowledge repo (required)")
	reportFile := fs.String("report", "", "gate report JSON `file` from `okf-operator gate --review`, or - for stdin (required)")
	var app github.AppFlags
	fs.StringVar(&app.AppID, reviewCredentials.AppIDFlag, "", "review GitHub App ID (default $"+EnvReviewAppID+")")
	fs.StringVar(&app.KeyFile, reviewCredentials.KeyFileFlag, "", "review GitHub App private key PEM file (default $"+EnvReviewAppKeyFile+")")
	if err := parseFlags(fs, args, env.Stdout, env.Stderr); err != nil {
		return err
	}
	usage := func(err error) error {
		_, _ = fmt.Fprintln(env.Stderr, err)
		fs.SetOutput(env.Stderr)
		fs.Usage()
		return UsageError{}
	}
	if err := checkPRFlags(fs, *kbRepo, *pr, "report", *reportFile); err != nil {
		return usage(err)
	}
	cfg, err := github.LoadNamed(app, reviewCredentials, os.Getenv)
	if err != nil {
		return usage(err)
	}

	secrets := &github.Secrets{}
	// Not used here, but never let it reach a log.
	secrets.Add(os.Getenv(github.EnvToken))
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	fail := func(err error) error { return errors.New(secrets.Redact(err.Error())) }

	report, err := readGateReport(*reportFile, env.Stdin)
	if err != nil {
		return fail(err)
	}
	clients, err := github.NewProvider(cfg, github.Access{
		Repos: []string{*kbRepo},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("read"),
			Metadata:     new("read"),
			PullRequests: new("write"),
		},
	}, secrets, logger)
	if err != nil {
		return fail(err)
	}
	out, err := approve.Run(ctx, approve.GitHub{Clients: clients}, *kbRepo, *pr, report)
	if err != nil {
		return fail(err)
	}
	logger.InfoContext(ctx, "approval decided", "pr", out.PR, "head_sha", out.HeadSHA, "approved", out.Approved, "reason", out.Reason)
	return approve.Encode(env.Stdout, out)
}

func readGateReport(file string, stdin io.Reader) (*gate.Report, error) {
	if file == "-" {
		return gate.Decode(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read gate report: %w", err)
	}
	defer func() { _ = f.Close() }()
	return gate.Decode(f)
}
