package cli

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"runtime/debug"
)

// BuildInfo is set through -ldflags by the Makefile and the Dockerfile.
type BuildInfo struct {
	Version string
	Commit  string
}

var versionCommand = Command{
	Name:    "version",
	Summary: "print the version, commit and Go version",
	Run:     runVersion,
}

func runVersion(_ context.Context, env Env, args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	if err := parseFlags(fs, args, env.Stdout, env.Stderr); err != nil {
		return err
	}
	b := env.Build.withVCSCommit()
	_, _ = fmt.Fprintf(env.Stdout, "okf-operator %s (commit %s, %s)\n", b.Version, b.Commit, runtime.Version())
	return nil
}

// withVCSCommit fills in the commit from the VCS info the Go toolchain embeds,
// for builds without -ldflags such as `go install`.
func (b BuildInfo) withVCSCommit() BuildInfo {
	if b.Commit != "" && b.Commit != "none" {
		return b
	}
	b.Commit = "none"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			b.Commit = s.Value
			if len(b.Commit) > 7 {
				b.Commit = b.Commit[:7]
			}
		}
	}
	return b
}
