package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/review"
)

var gateCommand = Command{
	Name:    "gate",
	Summary: "evaluate a knowledge PR against the auto-merge rules in .kb/policy.yaml",
	Run:     runGate,
}

func runGate(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator gate --knowledge-repo owner/name --pr <number> --plan <file|-> [--review <file>] [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	kbRepo := fs.String("knowledge-repo", "", "knowledge repo `owner/name` the PR belongs to (required)")
	pr := fs.Int("pr", 0, "pull request `number` in the knowledge repo (required)")
	planFile := fs.String("plan", "", "plan JSON `file` of the run that produced the PR, or - for stdin (required)")
	reviewFile := fs.String("review", "", "result JSON `file` of `okf-operator review` for the same PR head, added as the review rule (optional)")
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
	if err := checkGateFlags(fs, *kbRepo, *pr, *planFile); err != nil {
		return usage(err)
	}
	cfg, err := github.Load(app, os.Getenv)
	if err != nil {
		return usage(err)
	}

	p, err := readPlan(*planFile, env.Stdin)
	if err != nil {
		return err
	}
	var rv *gate.Review
	if *reviewFile != "" {
		if rv, err = readReview(*reviewFile); err != nil {
			return err
		}
	}

	secrets := &github.Secrets{}
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	kbClients, err := github.NewProvider(cfg, github.Access{
		Repos: []string{*kbRepo},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("read"),
			Metadata:     new("read"),
			PullRequests: new("read"),
		},
	}, secrets, logger)
	if err != nil {
		return err
	}
	srcClients, err := github.NewProvider(cfg, github.ReadContents(p.Source), secrets, logger)
	if err != nil {
		return err
	}

	r, err := gate.Evaluate(ctx, gate.Deps{
		Reader:  gate.GitHubReader{Clients: kbClients},
		Commits: gate.GitHubCommits{Clients: srcClients},
		Review:  rv,
	}, *kbRepo, *pr, p)
	if err != nil {
		return errors.New(secrets.Redact(err.Error()))
	}
	return gate.Encode(env.Stdout, r)
}

func readReview(file string) (*gate.Review, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read review: %w", err)
	}
	defer func() { _ = f.Close() }()
	r, err := review.Decode(f)
	if err != nil {
		return nil, err
	}
	in := r.GateInput()
	return &in, nil
}

func checkGateFlags(fs *flag.FlagSet, kbRepo string, pr int, planFile string) error {
	return checkPRFlags(fs, kbRepo, pr, "plan", planFile)
}

// checkPRFlags checks the flags of a command that reads one knowledge PR and
// one required input file named by fileFlag.
func checkPRFlags(fs *flag.FlagSet, kbRepo string, pr int, fileFlag, file string) error {
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	for _, f := range []struct{ name, value string }{{"knowledge-repo", kbRepo}, {fileFlag, file}} {
		if f.value == "" {
			errs = append(errs, fmt.Errorf("missing required flag --%s", f.name))
		}
	}
	if owner, name, ok := strings.Cut(kbRepo, "/"); kbRepo != "" && (!ok || owner == "" || name == "" || strings.Contains(name, "/")) {
		errs = append(errs, fmt.Errorf("--knowledge-repo %q is not owner/name", kbRepo))
	}
	if pr <= 0 {
		errs = append(errs, errors.New("missing required flag --pr: want a positive pull request number"))
	}
	return errors.Join(errs...)
}
