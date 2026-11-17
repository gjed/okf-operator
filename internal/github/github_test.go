package github

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		flags   AppFlags
		env     map[string]string
		want    Config
		wantErr string
	}{
		{"app from flags wins over token", AppFlags{AppID: "7", KeyFile: "k.pem"}, map[string]string{EnvToken: "t"},
			Config{APIURL: defaultAPIURL, App: &AppConfig{ID: 7, KeyFile: "k.pem"}}, ""},
		{"app from env", AppFlags{}, map[string]string{EnvAppID: "7", EnvAppKeyFile: "k.pem", EnvAPIURL: "http://x/"},
			Config{APIURL: "http://x/", App: &AppConfig{ID: 7, KeyFile: "k.pem"}}, ""},
		{"token fallback", AppFlags{}, map[string]string{EnvToken: "t"}, Config{APIURL: defaultAPIURL, Token: "t"}, ""},
		{"no credentials", AppFlags{}, nil, Config{}, "--app-id and --app-private-key-file"},
		{"only app id", AppFlags{AppID: "7"}, map[string]string{EnvToken: "t"}, Config{}, "private key file is not"},
		{"only key file", AppFlags{}, map[string]string{EnvAppKeyFile: "k.pem"}, Config{}, "App ID is not"},
		{"bad app id", AppFlags{AppID: "x", KeyFile: "k"}, nil, Config{}, "invalid GitHub App ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.flags, env(tt.env))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	_, err := Load(AppFlags{}, env(nil))
	if !errors.Is(err, ErrNoCredentials) || !strings.Contains(err.Error(), EnvToken) {
		t.Fatalf("no credentials error must name both options: %v", err)
	}
}

func TestAppFlags(t *testing.T) {
	var f AppFlags
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	f.Register(fs)
	if err := fs.Parse([]string{"--app-id", "3", "--app-private-key-file", "k"}); err != nil {
		t.Fatal(err)
	}
	if f.AppID != "3" || f.KeyFile != "k" {
		t.Fatalf("%+v", f)
	}
}

func TestTokenMode(t *testing.T) {
	fake := githubtest.New(t)
	p, err := NewProvider(Config{APIURL: fake.APIURL(), Token: "ghp_localToken123"}, ReadContents("acme/api"), &Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.ClientFor(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = c.Apps.GetRepositoryInstallation(context.Background(), "acme", "api")
	if got := fake.Auths(); len(got) != 1 || got[0] != "Bearer ghp_localToken123" {
		t.Fatalf("auths = %v", got)
	}
	if len(fake.TokenRequests()) != 0 {
		t.Fatal("token mode must not mint installation tokens")
	}
}

func TestAppModeNarrowsAndCachesPerOwner(t *testing.T) {
	fake := githubtest.New(t)
	keyFile, _ := githubtest.WriteKey(t)
	cfg := Config{APIURL: fake.APIURL(), App: &AppConfig{ID: 7, KeyFile: keyFile}}
	p, err := NewProvider(cfg, ReadContents("acme/api", "acme/kb", "other/lib"), &Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, owner := range []string{"acme", "ACME", "other"} {
		if _, err := p.ClientFor(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	reqs := fake.TokenRequests()
	if len(reqs) != 2 {
		t.Fatalf("want one token per owner, got %d", len(reqs))
	}
	perms := map[string]any{"contents": "read", "metadata": "read"}
	want := []map[string]any{
		{"repositories": []any{"api", "kb"}, "permissions": perms},
		{"repositories": []any{"lib"}, "permissions": perms},
	}
	for i, r := range reqs {
		if !reflect.DeepEqual(r.Body, want[i]) {
			t.Errorf("token request %d body = %v, want %v", i, r.Body, want[i])
		}
		if r.InstallationID != "42" || !strings.HasPrefix(r.Auth, "Bearer ey") {
			t.Errorf("token request %d: installation %s auth %q", i, r.InstallationID, r.Auth)
		}
	}
}

func TestAppModeBadKey(t *testing.T) {
	keyFile := t.TempDir() + "/missing.pem"
	if _, err := NewProvider(Config{App: &AppConfig{ID: 1, KeyFile: keyFile}}, Access{}, &Secrets{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("want error for a missing key file")
	}
}

// TestSecretsNeverLogged runs App mode at debug level and logs the secrets on
// purpose; none may reach the output.
func TestSecretsNeverLogged(t *testing.T) {
	fake := githubtest.New(t)
	keyFile, pem := githubtest.WriteKey(t)
	var buf bytes.Buffer
	secrets := &Secrets{}
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}), secrets))

	p, err := NewProvider(Config{APIURL: fake.APIURL(), App: &AppConfig{ID: 7, KeyFile: keyFile}}, ReadContents("acme/api"), secrets, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClientFor(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	jwtAuth := fake.TokenRequests()[0].Auth
	appJWT := strings.TrimPrefix(jwtAuth, "Bearer ")
	keyLine := strings.Split(pem, "\n")[3]

	logger.Debug("token "+fake.InstallationToken, "key", pem, "jwt", appJWT)
	logger.With("tok", fake.InstallationToken).WithGroup("g").Info("x", "err", errors.New("leak "+fake.InstallationToken), "line", keyLine)
	logger.Info("nested", slog.Group("auth", "header", jwtAuth))

	out := buf.String()
	if !strings.Contains(out, "minted installation token") {
		t.Fatalf("debug log missing; output:\n%s", out)
	}
	for name, secret := range map[string]string{"token": fake.InstallationToken, "jwt": appJWT, "pem line": keyLine} {
		if strings.Contains(out, secret) {
			t.Errorf("%s leaked into logs:\n%s", name, out)
		}
	}
	if strings.Count(out, redacted) < 6 {
		t.Errorf("expected redaction markers:\n%s", out)
	}
}
