// Package policy loads and validates the auto-merge policy (.kb/policy.yaml)
// that `gate` evaluates knowledge PRs against.
package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Version is the only policy version this build reads.
const Version = 1

// DefaultPath is the policy location inside the knowledge repo.
const DefaultPath = ".kb/policy.yaml"

// Policy is an auto-merge policy, version 1.
type Policy struct {
	Version   int       `yaml:"version"`
	AutoMerge AutoMerge `yaml:"auto_merge"`
	Limits    Limits    `yaml:"limits"`
}

// AutoMerge is the allowlist. An empty list allows nothing.
type AutoMerge struct {
	Types   []string `yaml:"types"`
	Bundles []string `yaml:"bundles"`
}

// Limits caps the diff size in changed lines (added plus removed).
type Limits struct {
	PageLines int `yaml:"page_lines"`
	PRLines   int `yaml:"pr_lines"`
}

// Parse decodes and validates a policy. The version is checked before strict
// decoding, so a future version reports the version, not its new keys.
func Parse(data []byte) (*Policy, error) {
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
	var p Policy
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty file")
		}
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks every rule and returns all violations joined.
func (p *Policy) Validate() error {
	var errs []error
	errs = append(errs, checkList("auto_merge.types", p.AutoMerge.Types, func(string) error { return nil })...)
	errs = append(errs, checkList("auto_merge.bundles", p.AutoMerge.Bundles, func(b string) error {
		if path.IsAbs(b) || b == "." || path.Clean(b) != b || slices.Contains(strings.Split(b, "/"), "..") {
			return errors.New("is not a relative directory")
		}
		return nil
	})...)
	if p.Limits.PageLines <= 0 {
		errs = append(errs, fmt.Errorf("limits.page_lines must be a positive integer, got %d", p.Limits.PageLines))
	}
	if p.Limits.PRLines <= 0 {
		errs = append(errs, fmt.Errorf("limits.pr_lines must be a positive integer, got %d", p.Limits.PRLines))
	}
	return errors.Join(errs...)
}

func checkList(field string, items []string, check func(string) error) []error {
	var errs []error
	seen := map[string]bool{}
	for i, v := range items {
		switch {
		case v == "":
			errs = append(errs, fmt.Errorf("%s[%d]: empty entry", field, i))
			continue
		case seen[v]:
			errs = append(errs, fmt.Errorf("%s[%d]: duplicate entry %q", field, i, v))
		}
		seen[v] = true
		if err := check(v); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %q %w", field, i, v, err))
		}
	}
	return errs
}

// AllowsType reports whether pages of concept type t may auto-merge.
func (p *Policy) AllowsType(t string) bool { return slices.Contains(p.AutoMerge.Types, t) }

// AllowsBundle reports whether pages in bundle b may auto-merge.
func (p *Policy) AllowsBundle(b string) bool { return slices.Contains(p.AutoMerge.Bundles, b) }
