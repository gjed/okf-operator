package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runCLI(t *testing.T, build BuildInfo, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), args, &out, &errOut, build)
	return code, out.String(), errOut.String()
}

// withCommand registers c for the duration of the test.
func withCommand(t *testing.T, c Command) {
	t.Helper()
	saved := commands
	commands = append(append([]Command{}, commands...), c)
	t.Cleanup(func() { commands = saved })
}

func TestRunExitCodes(t *testing.T) {
	withCommand(t, Command{
		Name:    "boom",
		Summary: "always fails",
		Run: func(context.Context, Env, []string) error {
			return errors.New("boom")
		},
	})
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"no command", nil, 2, "missing command"},
		{"unknown command", []string{"frobnicate"}, 2, `unknown command "frobnicate"`},
		{"bad subcommand flag", []string{"version", "-nope"}, 2, "flag provided but not defined"},
		{"bad global flag", []string{"--nope", "version"}, 2, "flag provided but not defined"},
		{"bad log level", []string{"--log-level", "verbose", "version"}, 2, `invalid --log-level "verbose"`},
		{"bad log format", []string{"--log-format", "xml", "version"}, 2, `invalid --log-format "xml"`},
		{"runtime error", []string{"boom"}, 1, "boom"},
		{"success", []string{"version"}, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runCLI(t, BuildInfo{Version: "dev", Commit: "none"}, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr %q)", code, tt.wantCode, errOut)
			}
			if !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("stderr %q does not contain %q", errOut, tt.wantErr)
			}
		})
	}
}

func TestUsageErrorsPrintUsageToStderr(t *testing.T) {
	_, out, errOut := runCLI(t, BuildInfo{}, "frobnicate")
	if out != "" {
		t.Fatalf("stdout = %q, want empty", out)
	}
	for _, c := range commands {
		if !strings.Contains(errOut, c.Name) {
			t.Fatalf("stderr does not list command %q: %q", c.Name, errOut)
		}
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		code, out, _ := runCLI(t, BuildInfo{}, args...)
		if code != 0 {
			t.Fatalf("%v: exit code = %d, want 0", args, code)
		}
		for _, c := range append(commands, Command{Name: "help"}) {
			if !strings.Contains(out, c.Name) {
				t.Fatalf("%v: stdout does not list %q: %q", args, c.Name, out)
			}
		}
	}
}

func TestSubcommandHelpPrintsFlags(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "version", "-h")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "Usage of version") {
		t.Fatalf("stdout = %q, want the version flags", out)
	}
}

func TestVersionReleaseBuild(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{Version: "1.2.3", Commit: "abc1234"}, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, want := range []string{"1.2.3", "abc1234", runtime.Version()} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout %q does not contain %q", out, want)
		}
	}
}

func TestVersionDevBuild(t *testing.T) {
	_, out, _ := runCLI(t, BuildInfo{Version: "dev", Commit: "none"}, "version")
	if !strings.HasPrefix(out, "okf-operator dev ") {
		t.Fatalf("stdout = %q, want version dev", out)
	}
}

func TestJSONLogs(t *testing.T) {
	withCommand(t, Command{
		Name:    "log",
		Summary: "writes one log line",
		Run: func(_ context.Context, env Env, _ []string) error {
			env.Logger.Info("hello")
			return nil
		},
	})
	_, _, errOut := runCLI(t, BuildInfo{}, "--log-format", "json", "log")
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(errOut)), &line); err != nil {
		t.Fatalf("stderr %q is not a JSON object: %v", errOut, err)
	}
	for _, key := range []string{"time", "level", "msg"} {
		if _, ok := line[key]; !ok {
			t.Fatalf("log line %v has no %q", line, key)
		}
	}
}

func TestRunWithoutCollectorIsFast(t *testing.T) {
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "OTEL_") {
			t.Setenv(key, "")
		}
	}
	start := time.Now()
	code, _, errOut := runCLI(t, BuildInfo{Version: "dev", Commit: "none"}, "version")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr %q", code, errOut)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("run took %s without a collector, want under 1s", elapsed)
	}
}
