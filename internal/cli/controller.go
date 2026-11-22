package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gjed/okf-operator/internal/controller"
	"github.com/gjed/okf-operator/internal/github"
)

var controllerCommand = Command{
	Name:    "controller",
	Summary: "run the pipeline as a Kubernetes operator for KnowledgeBase and Source resources",
	Run:     runController,
}

func runController(ctx context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("controller", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), "Usage: okf-operator controller [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	webhookAddr := fs.String("webhook-bind-address", ":8080", "`address` the GitHub webhook receiver listens on (POST /webhook)")
	healthAddr := fs.String("health-probe-bind-address", ":8081", "`address` of the /healthz and /readyz probes")
	leaderElect := fs.Bool("leader-elect", true, "elect one active replica through a Lease")
	leaderNS := fs.String("leader-election-namespace", "", "`namespace` of the leader election Lease (default: the Pod's namespace)")
	namespace := fs.String("namespace", "", "watch only this `namespace` (default: all namespaces)")
	concurrency := fs.Int("concurrency", 4, "most Sources run at once; Sources of one knowledge repo never overlap")
	if err := parseFlags(fs, args, env.Stdout, env.Stderr); err != nil {
		return err
	}
	var errs []error
	if fs.NArg() > 0 {
		errs = append(errs, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	if *concurrency < 1 {
		errs = append(errs, fmt.Errorf("--concurrency %d must be at least 1", *concurrency))
	}
	if err := errors.Join(errs...); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		fs.SetOutput(env.Stderr)
		fs.Usage()
		return UsageError{}
	}
	apiURL := os.Getenv(github.EnvAPIURL)
	if apiURL == "" {
		apiURL = "https://api.github.com/"
	}
	version := env.Build.Version
	if version == "" {
		version = "dev"
	}
	return controller.Run(ctx, controller.Options{
		WebhookAddr:             *webhookAddr,
		HealthAddr:              *healthAddr,
		LeaderElect:             *leaderElect,
		LeaderElectionNamespace: *leaderNS,
		Namespace:               *namespace,
		Concurrency:             *concurrency,
		APIURL:                  apiURL,
		ToolVersion:             version,
		Logger:                  env.Logger,
	})
}
