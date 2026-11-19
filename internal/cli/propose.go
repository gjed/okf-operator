package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/propose"
	"github.com/gjed/okf-operator/internal/write"
)

var proposeCommand = Command{
	Name:    "propose",
	Summary: "open or update the knowledge PR for a write report and advance the source cursor",
	Run:     runPropose,
}

func runPropose(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("propose", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator propose --plan <file> --report <file|-> --knowledge-repo owner/name --knowledge-dir <path> --knowledge-base <sha> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	planFile := fs.String("plan", "", "plan JSON `file` from `okf-operator plan` (required)")
	reportFile := fs.String("report", "", "write report JSON `file` from `okf-operator write`, or - for stdin (required)")
	kbRepo := fs.String("knowledge-repo", "", "knowledge repo `owner/name` the PR is opened in (required)")
	kbDir := fs.String("knowledge-dir", "", "`path` to the knowledge checkout write ran against (required)")
	kbBase := fs.String("knowledge-base", "", "full commit `sha` the knowledge checkout is at, e.g. $(git -C <path> rev-parse HEAD) (required)")
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
	if err := checkProposeFlags(fs, *planFile, *reportFile, *kbRepo, *kbDir, *kbBase); err != nil {
		return usage(err)
	}
	cfg, err := github.Load(app, os.Getenv)
	if err != nil {
		return usage(err)
	}

	secrets := &github.Secrets{}
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	fail := func(err error) error { return errors.New(secrets.Redact(err.Error())) }

	p, err := readPlan(*planFile, nil)
	if err != nil {
		return fail(err)
	}
	rep, err := readReport(*reportFile, env.Stdin)
	if err != nil {
		return fail(err)
	}
	// Fail on a mismatched plan and report before minting any token.
	if err := propose.Agree(p, rep); err != nil {
		return fail(err)
	}
	knowledge, err := github.NewProvider(cfg, knowledgeAccess(*kbRepo), secrets, logger)
	if err != nil {
		return fail(err)
	}
	source, err := github.NewProvider(cfg, sourceAccess(p.Source), secrets, logger)
	if err != nil {
		return fail(err)
	}
	res, err := propose.Run(ctx, p, rep, propose.Deps{
		Knowledge:     propose.GitHubKnowledge{Clients: knowledge, Repo: *kbRepo},
		SourcePRs:     propose.GitHubSourcePRs{Differ: plan.GitHubDiffer{Clients: source}},
		Cursor:        cursor.Writer{Clients: knowledge, KnowledgeRepo: *kbRepo},
		KnowledgeRepo: *kbRepo,
		KnowledgeDir:  *kbDir,
		Base:          *kbBase,
		Logger:        logger,
	})
	if err != nil {
		return fail(err)
	}
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}

// knowledgeAccess is what propose needs on the knowledge repo: push the bot
// branch and kb-state, and open or update the bot PR.
func knowledgeAccess(repo string) github.Access {
	return github.Access{
		Repos: []string{repo},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("write"),
			Metadata:     new("read"),
			PullRequests: new("write"),
		},
	}
}

// sourceAccess is what propose needs on the source repo: list the range's
// commits and the merged PRs behind them. It gets its own token, so a source
// repo of the same owner never sees the knowledge repo's write permissions.
func sourceAccess(repo string) github.Access {
	return github.Access{
		Repos: []string{repo},
		Permissions: gogithub.InstallationPermissions{
			Contents:     new("read"),
			Metadata:     new("read"),
			PullRequests: new("read"),
		},
	}
}

func readReport(file string, stdin io.Reader) (*write.Report, error) {
	if file == "-" {
		return propose.DecodeReport(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	defer func() { _ = f.Close() }()
	return propose.DecodeReport(f)
}

func checkProposeFlags(fs *flag.FlagSet, planFile, reportFile, kbRepo, kbDir, kbBase string) error {
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	for _, f := range []struct{ name, value string }{
		{"plan", planFile}, {"report", reportFile}, {"knowledge-repo", kbRepo}, {"knowledge-dir", kbDir}, {"knowledge-base", kbBase},
	} {
		if f.value == "" {
			errs = append(errs, fmt.Errorf("missing required flag --%s", f.name))
		}
	}
	if planFile == "-" {
		errs = append(errs, errors.New("--plan must be a file: only --report reads stdin"))
	}
	if owner, name, ok := strings.Cut(kbRepo, "/"); kbRepo != "" && (!ok || owner == "" || name == "" || strings.Contains(name, "/")) {
		errs = append(errs, fmt.Errorf("--knowledge-repo %q is not owner/name", kbRepo))
	}
	if kbBase != "" && !cursor.ValidSHA(kbBase) {
		errs = append(errs, fmt.Errorf("--knowledge-base %q is not a full 40-character lowercase hex commit SHA", kbBase))
	}
	return errors.Join(errs...)
}
