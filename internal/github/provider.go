package github

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	gogithub "github.com/google/go-github/v93/github"
)

const requestTimeout = 30 * time.Second

// Clients hands out a GitHub client authorised for one repository owner.
type Clients interface {
	ClientFor(ctx context.Context, owner string) (*gogithub.Client, error)
}

// Access is what a command needs: the owner/name repositories it reads or
// writes and the permissions its installation tokens carry.
type Access struct {
	Repos       []string
	Permissions gogithub.InstallationPermissions
}

// ReadContents is the access `plan` needs: read-only contents and metadata.
func ReadContents(repos ...string) Access {
	return Access{
		Repos: repos,
		Permissions: gogithub.InstallationPermissions{
			Contents: new("read"),
			Metadata: new("read"),
		},
	}
}

// Provider implements Clients for a token or a GitHub App.
type Provider struct {
	cfg     Config
	access  Access
	secrets *Secrets
	logger  *slog.Logger
	http    *http.Client
	key     *rsa.PrivateKey
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]*gogithub.Client
}

// NewProvider validates cfg and registers its secrets. In App mode it reads
// and parses the private key; no token is minted until ClientFor is called.
func NewProvider(cfg Config, access Access, secrets *Secrets, logger *slog.Logger) (*Provider, error) {
	p := &Provider{
		cfg:     cfg,
		access:  access,
		secrets: secrets,
		logger:  logger,
		http:    &http.Client{Timeout: requestTimeout},
		now:     time.Now,
		cache:   map[string]*gogithub.Client{},
	}
	if cfg.App == nil {
		secrets.Add(cfg.Token)
		return p, nil
	}
	pem, err := os.ReadFile(cfg.App.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("GitHub App private key: %w", err)
	}
	secrets.Add(string(pem))
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
	if err != nil {
		// The parser error never carries key material, but keep it out anyway.
		return nil, fmt.Errorf("GitHub App private key %s: not a valid RSA PEM key", cfg.App.KeyFile)
	}
	p.key = key
	return p, nil
}

// ClientFor returns a client for owner. In App mode it mints one installation
// token per owner, narrowed to the owner's repositories in Access, and caches
// it in memory for the life of the Provider.
func (p *Provider) ClientFor(ctx context.Context, owner string) (*gogithub.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := strings.ToLower(owner)
	if c, ok := p.cache[key]; ok {
		return c, nil
	}
	token := p.cfg.Token
	if p.cfg.App != nil {
		var err error
		if token, err = p.mint(ctx, owner); err != nil {
			return nil, err
		}
	}
	c, err := p.client(token)
	if err != nil {
		return nil, err
	}
	p.cache[key] = c
	return c, nil
}

func (p *Provider) client(token string) (*gogithub.Client, error) {
	return gogithub.NewClient(
		gogithub.WithHTTPClient(p.http),
		gogithub.WithAuthToken(token),
		gogithub.WithURLs(&p.cfg.APIURL, nil),
	)
}

// mint finds the App installation for owner and creates a token limited to
// the owner's repositories and the requested permissions.
func (p *Provider) mint(ctx context.Context, owner string) (string, error) {
	var names []string
	for _, r := range p.access.Repos {
		o, n, _ := strings.Cut(r, "/")
		if strings.EqualFold(o, owner) {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 {
		return "", fmt.Errorf("no repository of %s in the command's access list", owner)
	}

	appJWT, err := p.appJWT()
	if err != nil {
		return "", err
	}
	app, err := p.client(appJWT)
	if err != nil {
		return "", err
	}
	inst, _, err := app.Apps.GetRepositoryInstallation(ctx, owner, names[0])
	if err != nil {
		return "", fmt.Errorf("find GitHub App installation for %s/%s: %w", owner, names[0], err)
	}
	perms := p.access.Permissions
	tok, _, err := app.Apps.CreateInstallationToken(ctx, inst.GetID(), &gogithub.InstallationTokenOptions{
		Repositories: names,
		Permissions:  &perms,
	})
	if err != nil {
		return "", fmt.Errorf("create installation token for %s: %w", owner, err)
	}
	p.secrets.Add(tok.GetToken())
	p.logger.DebugContext(ctx, "minted installation token",
		"owner", owner, "installation_id", inst.GetID(), "repositories", names,
		"expires_at", tok.GetExpiresAt().Time)
	return tok.GetToken(), nil
}

// appJWT signs a 9-minute App JWT, backdated 60s for clock drift.
func (p *Provider) appJWT() (string, error) {
	now := p.now()
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(p.cfg.App.ID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}).SignedString(p.key)
	if err != nil {
		return "", fmt.Errorf("sign GitHub App JWT: %w", err)
	}
	p.secrets.Add(s)
	return s, nil
}

// IsNotFound reports whether err is a GitHub 404.
func IsNotFound(err error) bool {
	var e *gogithub.ErrorResponse
	return errors.As(err, &e) && e.Response != nil && e.Response.StatusCode == http.StatusNotFound
}
