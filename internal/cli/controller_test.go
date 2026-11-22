package cli

import (
	"strings"
	"testing"
)

func TestControllerHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "controller", "-h")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, f := range []string{"--webhook-bind-address", "--health-probe-bind-address", "--leader-elect", "--namespace", "--concurrency"} {
		if !strings.Contains(out, strings.TrimPrefix(f, "-")) {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
}

func TestControllerUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"controller", "--frobnicate"},
		{"controller", "--concurrency", "0"},
		{"controller", "extra"},
	} {
		code, out, errOut := runCLI(t, BuildInfo{}, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "Usage: okf-operator controller") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
