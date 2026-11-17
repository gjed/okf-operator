// Command okf-operator keeps an Open Knowledge Format bundle repository in sync
// with merged pull requests in source repositories.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gjed/okf-operator/internal/cli"
)

// Set through -ldflags by the Makefile and the Dockerfile.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.BuildInfo{Version: version, Commit: commit})
	stop()
	os.Exit(code)
}
