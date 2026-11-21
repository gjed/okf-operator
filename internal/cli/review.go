package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/review"
)

// Reviewer model environment variables read by review. The writer's
// OKF_MODEL_* settings are never a fallback.
const (
	EnvReviewModelBaseURL = "OKF_REVIEW_MODEL_BASE_URL"
	EnvReviewModel        = "OKF_REVIEW_MODEL"
	EnvReviewModelAPIKey  = "OKF_REVIEW_MODEL_API_KEY"
)

var reviewCommand = Command{
	Name:    "review",
	Summary: "ask a reviewer model whether a knowledge PR's changes are supported by the source diff",
	Run:     runReview,
}

func runReview(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator review --knowledge-repo owner/name --pr <number> --plan <file|-> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	kbRepo := fs.String("knowledge-repo", "", "knowledge repo `owner/name` the PR belongs to (required)")
	pr := fs.Int("pr", 0, "pull request `number` in the knowledge repo (required)")
	planFile := fs.String("plan", "", "plan JSON `file` of the run that produced the PR, or - for stdin (required)")
	baseURL := fs.String("review-model-base-url", "", "OpenAI-compatible API base `url` of the reviewer (default $"+EnvReviewModelBaseURL+")")
	modelName := fs.String("review-model", "", "reviewer model `name` (default $"+EnvReviewModel+")")
	maxTokens := fs.Int("max-tokens-per-page", 16000, "most model tokens for the review of one page")
	var app github.AppFlags
	app.Register(fs)
	if err := parseFlags(fs, args, env.Stdout, env.Stderr); err != nil {
		return err
	}
	usage := func(err error) error {
		_, _ = fmt.Fprintln(env.Stderr, err)
		fs.SetOutput(env.Stderr)
		fs.Usage()
		return UsageError{}
	}
	if *baseURL == "" {
		*baseURL = os.Getenv(EnvReviewModelBaseURL)
	}
	if *modelName == "" {
		*modelName = os.Getenv(EnvReviewModel)
	}
	errs := []error{checkGateFlags(fs, *kbRepo, *pr, *planFile)}
	if *baseURL == "" {
		errs = append(errs, fmt.Errorf("missing reviewer model endpoint: set --review-model-base-url or %s", EnvReviewModelBaseURL))
	}
	if *modelName == "" {
		errs = append(errs, fmt.Errorf("missing reviewer model name: set --review-model or %s", EnvReviewModel))
	}
	if *maxTokens < 1 {
		errs = append(errs, fmt.Errorf("--max-tokens-per-page %d must be at least 1", *maxTokens))
	}
	if err := errors.Join(errs...); err != nil {
		return usage(err)
	}
	cfg, err := github.Load(app, os.Getenv)
	if err != nil {
		return usage(err)
	}

	secrets := &github.Secrets{}
	apiKey := os.Getenv(EnvReviewModelAPIKey)
	secrets.Add(apiKey)
	// Neither is used here, but never let one reach a log.
	secrets.Add(os.Getenv(EnvModelAPIKey))
	secrets.Add(os.Getenv(github.EnvToken))
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	fail := func(err error) error { return errors.New(secrets.Redact(err.Error())) }

	p, err := readPlan(*planFile, env.Stdin)
	if err != nil {
		return fail(err)
	}
	kbClients, err := github.NewProvider(cfg, github.Access{
		Repos: []string{*kbRepo},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("read"),
			Metadata:     new("read"),
			PullRequests: new("read"),
		},
	}, secrets, logger)
	if err != nil {
		return fail(err)
	}
	srcClients, err := github.NewProvider(cfg, github.ReadContents(p.Source), secrets, logger)
	if err != nil {
		return fail(err)
	}
	r, err := review.Run(ctx, review.Deps{
		Reader:           gate.GitHubReader{Clients: kbClients},
		Source:           plan.GitHubDiffer{Clients: srcClients},
		Model:            &model.Client{BaseURL: *baseURL, Model: *modelName, APIKey: apiKey, Redact: secrets.Redact},
		ModelName:        *modelName,
		MaxTokensPerPage: *maxTokens,
		Logger:           logger,
	}, *kbRepo, *pr, p)
	if err != nil {
		return fail(err)
	}
	return review.Encode(env.Stdout, r)
}
