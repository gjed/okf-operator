// Package cli is the okf-operator dispatcher: global flags, subcommand lookup,
// usage output and the mapping from errors to exit codes.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/gjed/okf-operator/internal/telemetry"
)

// Command is one subcommand. Run parses args with its own flag set.
type Command struct {
	Name    string
	Summary string
	Run     func(ctx context.Context, env Env, args []string) error
}

// Env is set up once by the dispatcher and passed to every subcommand.
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Logger *slog.Logger
	Build  BuildInfo
}

// UsageError marks a usage mistake, which exits 2. A nil Err means the
// message was already printed by the subcommand.
type UsageError struct{ Err error }

func (e UsageError) Error() string {
	if e.Err == nil {
		return "usage error"
	}
	return e.Err.Error()
}

func (e UsageError) Unwrap() error { return e.Err }

// Run executes one invocation and returns the process exit code: 0 on success,
// 1 on a runtime failure and 2 on a usage error.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	err := run(ctx, args, stdout, stderr, build)
	var usage UsageError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &usage):
		if usage.Err != nil {
			_, _ = fmt.Fprintf(stderr, "okf-operator: %v\n", usage.Err)
			printUsage(stderr)
		}
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "okf-operator: %v\n", err)
		return 1
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) error {
	global := flag.NewFlagSet("okf-operator", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	logFormat := global.String("log-format", "text", "log format: text or json")
	logLevel := global.String("log-level", "info", "log level: debug, info, warn or error")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil
		}
		return UsageError{err}
	}

	rest := global.Args()
	if len(rest) == 0 {
		return UsageError{errors.New("missing command")}
	}
	name, cmdArgs := rest[0], rest[1:]
	if name == "help" {
		printUsage(stdout)
		return nil
	}
	cmd, ok := findCommand(name)
	if !ok {
		return UsageError{fmt.Errorf("unknown command %q", name)}
	}

	logger, err := telemetry.NewLogger(stderr, *logFormat, *logLevel)
	if err != nil {
		return UsageError{err}
	}
	shutdown, err := telemetry.Setup(ctx)
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(sctx); err != nil {
			logger.Warn("telemetry shutdown", "err", err)
		}
	}()

	ctx, span := otel.Tracer("okf-operator").Start(ctx, cmd.Name)
	env := Env{Stdout: stdout, Stderr: stderr, Logger: logger, Build: build}
	err = cmd.Run(ctx, env, cmdArgs)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
	return err
}

// parseFlags parses a subcommand's flags. -h prints them to stdout and returns
// flag.ErrHelp; a bad flag prints the error and them to stderr and returns a
// UsageError whose message is already printed.
func parseFlags(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) error {
	fs.SetOutput(io.Discard)
	err := fs.Parse(args)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, flag.ErrHelp):
		fs.SetOutput(stdout)
		fs.Usage()
		return err
	default:
		_, _ = fmt.Fprintln(stderr, err)
		fs.SetOutput(stderr)
		fs.Usage()
		return UsageError{}
	}
}

func findCommand(name string) (Command, bool) {
	for _, c := range commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, "Usage: okf-operator [--log-format text|json] [--log-level debug|info|warn|error] <command> [flags]\n\nCommands:\n")
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", c.Name, c.Summary)
	}
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", "help", "show this help")
	_, _ = fmt.Fprint(w, "\nRun 'okf-operator <command> -h' for the flags of a command.\n")
}
