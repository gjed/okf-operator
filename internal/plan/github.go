package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gjed/okf-operator/internal/github"
)

// MaxCompareFiles is the most files the compare API lists for one comparison.
const MaxCompareFiles = 300

// GitHubDiffer implements SourceDiffer with the GitHub compare API.
//
// SIMPLIFIED: the compare API lists at most 300 files, so a range with 300 or
// more changed files fails closed with ErrIncomplete and falls to reconcile.
// Upgrade path: a clone-based differ behind the same SourceDiffer interface.
type GitHubDiffer struct {
	Clients github.Clients
}

// Head returns the SHA at the head of repo's default branch.
func (g GitHubDiffer) Head(ctx context.Context, repo string) (string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return "", err
	}
	r, _, err := c.Repositories.Get(ctx, owner, name)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", repo, err)
	}
	sha, _, err := c.Repositories.GetCommitSHA1(ctx, owner, name, r.GetDefaultBranch(), "")
	if err != nil {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, r.GetDefaultBranch(), err)
	}
	return sha, nil
}

// Changes compares base...head. A 404 means base (or head) is not in the
// repo's history and counts as diverged.
func (g GitHubDiffer) Changes(ctx context.Context, repo, base, head string) ([]ChangedPath, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	cmp, _, err := c.Repositories.CompareCommits(ctx, owner, name, base, head, nil)
	if github.IsNotFound(err) {
		return nil, fmt.Errorf("%s %s...%s not found: %w", repo, base, head, ErrDiverged)
	}
	if err != nil {
		return nil, fmt.Errorf("compare %s %s...%s: %w", repo, base, head, err)
	}
	switch s := cmp.GetStatus(); s {
	case StatusIdentical:
		return []ChangedPath{}, nil
	case StatusAhead:
	case StatusBehind, StatusDiverged:
		return nil, fmt.Errorf("%s %s...%s is %s: %w", repo, base, head, s, ErrDiverged)
	default:
		return nil, fmt.Errorf("compare %s %s...%s: unknown status %q", repo, base, head, s)
	}
	if len(cmp.Files) >= MaxCompareFiles {
		return nil, fmt.Errorf("%s %s...%s lists %d files: %w", repo, base, head, len(cmp.Files), ErrIncomplete)
	}
	out := make([]ChangedPath, 0, len(cmp.Files))
	for _, f := range cmp.Files {
		out = append(out, ChangedPath{Path: f.GetFilename(), Status: f.GetStatus(), PreviousPath: f.GetPreviousFilename()})
	}
	return out, nil
}

// DirPages implements PageChecker on a local knowledge repo checkout.
type DirPages string

// Exists reports whether the page file exists under the checkout.
func (d DirPages) Exists(page string) (bool, error) {
	fi, err := os.Stat(filepath.Join(string(d), filepath.FromSlash(page)))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("check page %s: %w", page, err)
	case fi.IsDir():
		return false, fmt.Errorf("check page %s: is a directory", page)
	}
	return true, nil
}
