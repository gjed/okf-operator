// Package github builds authenticated GitHub REST clients for okf-operator:
// a GITHUB_TOKEN for local use, or short-lived GitHub App installation tokens
// narrowed to the repositories and permissions a command needs.
package github

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
)

// Environment variables read by Load.
const (
	EnvAppID          = "OKF_GITHUB_APP_ID"
	EnvAppKeyFile     = "OKF_GITHUB_APP_PRIVATE_KEY_FILE"
	EnvToken          = "GITHUB_TOKEN"
	EnvAPIURL         = "GITHUB_API_URL"
	defaultAPIURL     = "https://api.github.com/"
	credentialOptions = "set --app-id and --app-private-key-file (or " + EnvAppID + " and " + EnvAppKeyFile + ") for a GitHub App, or " + EnvToken
)

// ErrNoCredentials is returned by Load when neither App settings nor a token are set.
var ErrNoCredentials = errors.New("no GitHub credentials: " + credentialOptions)

// AppFlags are the GitHub App flags every command that calls GitHub binds.
type AppFlags struct {
	AppID   string
	KeyFile string
}

// Register adds --app-id and --app-private-key-file to fs.
func (f *AppFlags) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.AppID, "app-id", "", "GitHub App ID (default $"+EnvAppID+")")
	fs.StringVar(&f.KeyFile, "app-private-key-file", "", "GitHub App private key PEM file (default $"+EnvAppKeyFile+")")
}

// Config selects how to authenticate. Exactly one of App or Token is set.
type Config struct {
	APIURL string
	App    *AppConfig
	Token  string
}

// AppConfig is a GitHub App identity.
type AppConfig struct {
	ID      int64
	KeyFile string
}

// Load resolves credentials: App settings (flags, then env), then GITHUB_TOKEN.
// Every error it returns is a usage error.
func Load(f AppFlags, getenv func(string) string) (Config, error) {
	cfg := Config{APIURL: getenv(EnvAPIURL)}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	id, key := f.AppID, f.KeyFile
	if id == "" {
		id = getenv(EnvAppID)
	}
	if key == "" {
		key = getenv(EnvAppKeyFile)
	}
	switch {
	case id != "" && key != "":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("invalid GitHub App ID %q: want a positive integer", id)
		}
		cfg.App = &AppConfig{ID: n, KeyFile: key}
	case id != "":
		return Config{}, fmt.Errorf("GitHub App ID is set but the private key file is not: set --app-private-key-file or %s", EnvAppKeyFile)
	case key != "":
		return Config{}, fmt.Errorf("GitHub App private key file is set but the App ID is not: set --app-id or %s", EnvAppID)
	default:
		cfg.Token = getenv(EnvToken)
		if cfg.Token == "" {
			return Config{}, ErrNoCredentials
		}
	}
	return cfg, nil
}
