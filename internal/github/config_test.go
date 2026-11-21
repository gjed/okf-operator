package github

import (
	"strings"
	"testing"
)

var reviewNames = CredentialNames{
	AppIDFlag: "review-app-id", KeyFileFlag: "review-app-private-key-file",
	AppIDEnv: "OKF_REVIEW_GITHUB_APP_ID", KeyFileEnv: "OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE", TokenEnv: "OKF_REVIEW_GITHUB_TOKEN",
}

func TestLoadNamedReadsOnlyItsNames(t *testing.T) {
	writer := map[string]string{EnvAppID: "1", EnvAppKeyFile: "/w.pem", EnvToken: "ghp_writer"}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	_, err := LoadNamed(AppFlags{}, reviewNames, env(writer))
	if err == nil {
		t.Fatal("review names loaded the writer's credentials")
	}
	for _, w := range []string{"--review-app-id", "OKF_REVIEW_GITHUB_APP_ID", "OKF_REVIEW_GITHUB_TOKEN"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %s", err, w)
		}
	}

	both := map[string]string{"OKF_REVIEW_GITHUB_APP_ID": "9", "OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE": "/r.pem"}
	for k, v := range writer {
		both[k] = v
	}
	cfg, err := LoadNamed(AppFlags{}, reviewNames, env(both))
	if err != nil || cfg.App == nil || cfg.App.ID != 9 || cfg.App.KeyFile != "/r.pem" {
		t.Fatalf("review App config = %+v, %v", cfg.App, err)
	}
	if cfg, err := Load(AppFlags{}, env(both)); err != nil || cfg.App == nil || cfg.App.ID != 1 {
		t.Fatalf("default names config = %+v, %v: want the writer App", cfg.App, err)
	}

	cfg, err = LoadNamed(AppFlags{}, reviewNames, env(map[string]string{"OKF_REVIEW_GITHUB_TOKEN": "ghp_review", EnvToken: "ghp_writer"}))
	if err != nil || cfg.Token != "ghp_review" {
		t.Fatalf("review token = %q, %v", cfg.Token, err)
	}

	_, err = LoadNamed(AppFlags{}, reviewNames, env(map[string]string{"OKF_REVIEW_GITHUB_APP_ID": "9"}))
	if err == nil || !strings.Contains(err.Error(), "--review-app-private-key-file or OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE") {
		t.Fatalf("half App error = %v", err)
	}
}
