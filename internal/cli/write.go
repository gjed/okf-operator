package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

// Model environment variables read by write.
const (
	EnvModelBaseURL = "OKF_MODEL_BASE_URL"
	EnvModel        = "OKF_MODEL"
	EnvModelAPIKey  = "OKF_MODEL_API_KEY"
)

var writeCommand = Command{
	Name:    "write",
	Summary: "generate updated knowledge pages with provenance from a plan",
	Run:     runWrite,
}

func runWrite(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator write --plan <file|-> --knowledge-dir <path> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	planFile := fs.String("plan", "", "plan JSON `file` from `okf-operator plan`, or - for stdin (required)")
	kbDir := fs.String("knowledge-dir", "", "`path` to a checkout of the knowledge repo; changed pages are written here (required)")
	baseURL := fs.String("model-base-url", "", "OpenAI-compatible API base `url` (default $"+EnvModelBaseURL+")")
	modelName := fs.String("model", "", "model `name` (default $"+EnvModel+")")
	maxPages := fs.Int("max-pages", 10, "most update pages one run may send to the model")
	maxTokens := fs.Int("max-tokens-per-page", 16000, "most model tokens, all attempts together, for one page")
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
		*baseURL = os.Getenv(EnvModelBaseURL)
	}
	if *modelName == "" {
		*modelName = os.Getenv(EnvModel)
	}
	if err := checkWriteFlags(fs, *planFile, *kbDir, *baseURL, *modelName, *maxPages, *maxTokens); err != nil {
		return usage(err)
	}
	cfg, err := github.Load(app, os.Getenv)
	if err != nil {
		return usage(err)
	}

	secrets := &github.Secrets{}
	apiKey := os.Getenv(EnvModelAPIKey)
	secrets.Add(apiKey)
	// Unused in App mode, but never let it reach a log.
	secrets.Add(os.Getenv(github.EnvToken))
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	fail := func(err error) error { return errors.New(secrets.Redact(err.Error())) }

	p, err := readPlan(*planFile, env.Stdin)
	if err != nil {
		return fail(err)
	}
	clients, err := github.NewProvider(cfg, writeAccess(p.Source), secrets, logger)
	if err != nil {
		return fail(err)
	}
	b := env.Build
	if b.Version == "" {
		b.Version = "dev"
	}
	rep, err := write.Run(ctx, p, write.Deps{
		Source:           plan.GitHubDiffer{Clients: clients},
		Model:            &model.Client{BaseURL: *baseURL, Model: *modelName, APIKey: apiKey, Redact: secrets.Redact},
		Validator:        write.BaseValidator{},
		KnowledgeDir:     *kbDir,
		ModelName:        *modelName,
		ToolVersion:      b.Version,
		MaxPages:         *maxPages,
		MaxTokensPerPage: *maxTokens,
		Now:              time.Now,
		Logger:           logger,
	})
	if err != nil {
		return fail(err)
	}
	return write.Encode(env.Stdout, rep)
}

// writeAccess is what write needs on GitHub: read the source repo's contents
// and the PRs behind its commits. The knowledge repo is read from the checkout.
func writeAccess(source string) github.Access {
	return github.Access{
		Repos: []string{source},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("read"),
			Metadata:     new("read"),
			PullRequests: new("read"),
		},
	}
}

func readPlan(file string, stdin io.Reader) (*plan.Plan, error) {
	if file == "-" {
		return write.DecodePlan(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	defer func() { _ = f.Close() }()
	return write.DecodePlan(f)
}

func checkWriteFlags(fs *flag.FlagSet, planFile, kbDir, baseURL, modelName string, maxPages, maxTokens int) error {
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	for _, f := range []struct{ name, value string }{{"plan", planFile}, {"knowledge-dir", kbDir}} {
		if f.value == "" {
			errs = append(errs, fmt.Errorf("missing required flag --%s", f.name))
		}
	}
	if baseURL == "" {
		errs = append(errs, fmt.Errorf("missing model endpoint: set --model-base-url or %s", EnvModelBaseURL))
	}
	if modelName == "" {
		errs = append(errs, fmt.Errorf("missing model name: set --model or %s", EnvModel))
	}
	if maxPages < 1 {
		errs = append(errs, fmt.Errorf("--max-pages %d must be at least 1", maxPages))
	}
	if maxTokens < 1 {
		errs = append(errs, fmt.Errorf("--max-tokens-per-page %d must be at least 1", maxTokens))
	}
	return errors.Join(errs...)
}
