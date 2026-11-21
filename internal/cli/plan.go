package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/routing"
)

var planCommand = Command{
	Name:    "plan",
	Summary: "plan which knowledge pages a source commit range affects",
	Run:     runPlan,
}

func runPlan(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator plan --source owner/name --knowledge-repo owner/name --knowledge-dir <path> [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	source := fs.String("source", "", "source repo `owner/name` (required)")
	kbRepo := fs.String("knowledge-repo", "", "knowledge repo `owner/name` holding the kb-state branch (required)")
	kbDir := fs.String("knowledge-dir", "", "`path` to the knowledge repo checkout write will use: the open bot PR branch for the source, else the default branch (required)")
	routingFile := fs.String("routing", "", "routing map `file` (default <knowledge-dir>/"+routing.DefaultPath+")")
	to := fs.String("to", "", "plan up to this full 40-character commit `sha` instead of the source default branch HEAD")
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
	if err := checkPlanFlags(fs, *source, *kbRepo, *kbDir, *to); err != nil {
		return usage(err)
	}
	cfg, err := github.Load(app, os.Getenv)
	if err != nil {
		return usage(err)
	}

	secrets := &github.Secrets{}
	logger := slog.New(github.NewRedactingHandler(env.Logger.Handler(), secrets))
	clients, err := github.NewProvider(cfg, github.ReadContents(*source, *kbRepo), secrets, logger)
	if err != nil {
		return err
	}
	if *routingFile == "" {
		*routingFile = filepath.Join(*kbDir, filepath.FromSlash(routing.DefaultPath))
	}
	rm, err := routing.Load(*routingFile)
	if err != nil {
		return err
	}

	p, err := plan.Build(ctx, *source, *to, plan.Deps{
		Routing: rm,
		Cursor:  cursor.Reader{Clients: clients, KnowledgeRepo: *kbRepo},
		Differ:  plan.GitHubDiffer{Clients: clients},
		Pages:   plan.DirPages(*kbDir),
		Logger:  logger,
	})
	if err != nil {
		return errors.New(secrets.Redact(err.Error()))
	}
	return plan.Encode(env.Stdout, p)
}

func checkPlanFlags(fs *flag.FlagSet, source, kbRepo, kbDir, to string) error {
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	for _, f := range []struct{ name, value string }{{"source", source}, {"knowledge-repo", kbRepo}, {"knowledge-dir", kbDir}} {
		if f.value == "" {
			errs = append(errs, fmt.Errorf("missing required flag --%s", f.name))
		}
	}
	for _, f := range []struct{ name, value string }{{"source", source}, {"knowledge-repo", kbRepo}} {
		if owner, name, ok := strings.Cut(f.value, "/"); f.value != "" && (!ok || owner == "" || name == "" || strings.Contains(name, "/")) {
			errs = append(errs, fmt.Errorf("--%s %q is not owner/name", f.name, f.value))
		}
	}
	// --to becomes the next cursor, so it must be the same full SHA a cursor holds.
	if to != "" && !cursor.ValidSHA(to) {
		errs = append(errs, fmt.Errorf("--to %q is not a full 40-character lowercase hex commit SHA", to))
	}
	return errors.Join(errs...)
}
