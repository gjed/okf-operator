// Package routing loads and validates the routing map (.kb/routing.yaml) and
// matches changed source paths to knowledge pages.
package routing

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"
)

// Version is the only routing map version this build reads.
const Version = 1

// DefaultPath is the routing map location inside a knowledge repo checkout.
const DefaultPath = ".kb/routing.yaml"

// Map is a routing map, version 1.
type Map struct {
	Version int      `yaml:"version"`
	Sources []Source `yaml:"sources"`
}

// Source binds one source repo to one bundle and lists its routes.
type Source struct {
	Repo    string   `yaml:"repo"`
	Bundle  string   `yaml:"bundle"`
	Exclude []string `yaml:"exclude"`
	Routes  []Route  `yaml:"routes"`
}

// Route maps source path globs to one knowledge page. A non-empty Type allows
// the page to be created when it does not exist.
type Route struct {
	Page  string   `yaml:"page"`
	Type  string   `yaml:"type"`
	Paths []string `yaml:"paths"`
}

// Load reads and validates the routing map at path.
func Load(file string) (*Map, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the path is an operator-supplied flag
	if err != nil {
		return nil, fmt.Errorf("routing map: %w", err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("routing map %s: %w", file, err)
	}
	return m, nil
}

// Parse decodes and validates a routing map. The version is checked before
// strict decoding, so a future version reports the version, not its new keys.
func Parse(data []byte) (*Map, error) {
	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if header.Version != Version {
		return nil, fmt.Errorf("unsupported version %d: the supported version is %d", header.Version, Version)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Map
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty file")
		}
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks every rule and returns all violations joined.
func (m *Map) Validate() error {
	var errs []error
	seen := map[string]bool{}
	for i, s := range m.Sources {
		where := fmt.Sprintf("sources[%d] (%s)", i, s.Repo)
		if !validRepo(s.Repo) {
			errs = append(errs, fmt.Errorf("%s: repo %q is not owner/name", where, s.Repo))
		}
		key := strings.ToLower(s.Repo)
		if seen[key] {
			errs = append(errs, fmt.Errorf("%s: duplicate source %s", where, s.Repo))
		}
		seen[key] = true

		bundleOK := validBundle(s.Bundle)
		if !bundleOK {
			errs = append(errs, fmt.Errorf("%s: bundle %q is not a relative directory", where, s.Bundle))
		}
		for _, g := range s.Exclude {
			if !doublestar.ValidatePattern(g) {
				errs = append(errs, fmt.Errorf("%s: invalid exclude glob %q", where, g))
			}
		}
		for j, r := range s.Routes {
			rwhere := fmt.Sprintf("%s routes[%d]", where, j)
			if err := checkTarget(s.Bundle, r.Page, bundleOK); err != nil {
				errs = append(errs, fmt.Errorf("%s: source %s target %q: %w", rwhere, s.Repo, r.Page, err))
			}
			if len(r.Paths) == 0 {
				errs = append(errs, fmt.Errorf("%s: no paths", rwhere))
			}
			for _, g := range r.Paths {
				if !doublestar.ValidatePattern(g) {
					errs = append(errs, fmt.Errorf("%s: invalid glob %q", rwhere, g))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func validRepo(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

func validBundle(b string) bool {
	return b != "" && b != "." && !path.IsAbs(b) && path.Clean(b) == b && !hasDotDot(b)
}

func hasDotDot(p string) bool {
	return slices.Contains(strings.Split(p, "/"), "..")
}

// checkTarget enforces that a page is a relative .md path inside the bundle.
func checkTarget(bundle, page string, bundleOK bool) error {
	switch {
	case page == "":
		return errors.New("empty page")
	case path.IsAbs(page):
		return errors.New("absolute path")
	case hasDotDot(page):
		return errors.New(`".." segment`)
	case path.Ext(page) != ".md":
		return errors.New("not a .md page")
	case bundleOK && !strings.HasPrefix(path.Clean(page), bundle+"/"):
		return fmt.Errorf("outside bundle %q", bundle)
	}
	return nil
}

// Source returns the entry for repo, compared case-insensitively as GitHub
// does, or nil when the repo is not onboarded.
func (m *Map) Source(repo string) *Source {
	for i := range m.Sources {
		if strings.EqualFold(m.Sources[i].Repo, repo) {
			return &m.Sources[i]
		}
	}
	return nil
}

// PageMatch is one planned page and the changed paths that routed to it.
type PageMatch struct {
	Page        string
	Type        string
	SourcePaths []string
}

// Result is the outcome of matching changed paths. Every list is sorted.
type Result struct {
	Pages    []PageMatch
	Unrouted []string
	Excluded []string
}

// Match routes changed paths. Exclude globs are evaluated first; a path that
// matches several routes is routed to every matched page.
func (s *Source) Match(paths []string) Result {
	pages := map[string]*PageMatch{}
	res := Result{Pages: []PageMatch{}, Unrouted: []string{}, Excluded: []string{}}
	for _, p := range sortedUnique(paths) {
		if anyMatch(s.Exclude, p) {
			res.Excluded = append(res.Excluded, p)
			continue
		}
		routed := false
		for _, r := range s.Routes {
			if !anyMatch(r.Paths, p) {
				continue
			}
			routed = true
			pm := pages[r.Page]
			if pm == nil {
				pm = &PageMatch{Page: r.Page}
				pages[r.Page] = pm
			}
			if pm.Type == "" {
				pm.Type = r.Type
			}
			if !slices.Contains(pm.SourcePaths, p) {
				pm.SourcePaths = append(pm.SourcePaths, p)
			}
		}
		if !routed {
			res.Unrouted = append(res.Unrouted, p)
		}
	}
	for _, pm := range pages {
		res.Pages = append(res.Pages, *pm)
	}
	slices.SortFunc(res.Pages, func(a, b PageMatch) int { return strings.Compare(a.Page, b.Page) })
	return res
}

func anyMatch(globs []string, p string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, p); ok {
			return true
		}
	}
	return false
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
