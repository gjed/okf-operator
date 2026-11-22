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

// AppConfig is a GitHub App identity. Key, when set, is the PEM private key
// held in memory and KeyFile is ignored.
type AppConfig struct {
	ID      int64
	KeyFile string
	Key     []byte
}

// CredentialNames are the flags and environment variables one identity is
// read from. A command reads exactly one set, so two identities never mix.
type CredentialNames struct {
	AppIDFlag, KeyFileFlag string
	AppIDEnv, KeyFileEnv   string
	TokenEnv               string
}

// DefaultCredentials is the operator App, or GITHUB_TOKEN.
var DefaultCredentials = CredentialNames{
	AppIDFlag: "app-id", KeyFileFlag: "app-private-key-file",
	AppIDEnv: EnvAppID, KeyFileEnv: EnvAppKeyFile, TokenEnv: EnvToken,
}

// Load resolves credentials: App settings (flags, then env), then GITHUB_TOKEN.
// Every error it returns is a usage error.
func Load(f AppFlags, getenv func(string) string) (Config, error) {
	return LoadNamed(f, DefaultCredentials, getenv)
}

// LoadNamed resolves credentials like Load from the names in n: App settings
// (flags, then env), then the token variable. Every error it returns is a
// usage error and names n's options.
func LoadNamed(f AppFlags, n CredentialNames, getenv func(string) string) (Config, error) {
	cfg := Config{APIURL: getenv(EnvAPIURL)}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}
	id, key := f.AppID, f.KeyFile
	if id == "" {
		id = getenv(n.AppIDEnv)
	}
	if key == "" {
		key = getenv(n.KeyFileEnv)
	}
	switch {
	case id != "" && key != "":
		num, err := strconv.ParseInt(id, 10, 64)
		if err != nil || num <= 0 {
			return Config{}, fmt.Errorf("invalid GitHub App ID %q: want a positive integer", id)
		}
		cfg.App = &AppConfig{ID: num, KeyFile: key}
	case id != "":
		return Config{}, fmt.Errorf("GitHub App ID is set but the private key file is not: set --%s or %s", n.KeyFileFlag, n.KeyFileEnv)
	case key != "":
		return Config{}, fmt.Errorf("GitHub App private key file is set but the App ID is not: set --%s or %s", n.AppIDFlag, n.AppIDEnv)
	default:
		cfg.Token = getenv(n.TokenEnv)
		if cfg.Token != "" {
			return cfg, nil
		}
		if n == DefaultCredentials {
			return Config{}, ErrNoCredentials
		}
		return Config{}, fmt.Errorf("no GitHub credentials: set --%s and --%s (or %s and %s) for a GitHub App, or %s",
			n.AppIDFlag, n.KeyFileFlag, n.AppIDEnv, n.KeyFileEnv, n.TokenEnv)
	}
	return cfg, nil
}
