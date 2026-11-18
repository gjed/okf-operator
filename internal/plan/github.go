package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// MaxCompareFiles is the most files the compare API lists for one comparison.
const MaxCompareFiles = 300

// MaxCompareCommits is the most commits the compare API lists for one comparison.
const MaxCompareCommits = 250

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

// Changes compares base...head. A 404 with a resolvable head means base is not
// in the repo's history and counts as diverged; a missing head or an
// unreadable repo is an ordinary failure.
func (g GitHubDiffer) Changes(ctx context.Context, repo, base, head string) ([]ChangedPath, error) {
	cmp, err := g.compare(ctx, repo, base, head)
	if err != nil {
		return nil, err
	}
	out := make([]ChangedPath, 0, len(cmp.Files))
	for _, f := range cmp.Files {
		out = append(out, ChangedPath{Path: f.GetFilename(), Status: f.GetStatus(), PreviousPath: f.GetPreviousFilename()})
	}
	return out, nil
}

// Range is the source data write needs for base...head, all from one compare call.
type Range struct {
	// Patches maps each requested path to its compare patch. A rename is keyed
	// under its new and its previous filename. A path is absent when the API
	// omitted its patch (a changed file with no patch, e.g. binary or too large).
	Patches map[string]string
	// Commits lists the commit SHAs of the range, as the compare API lists them.
	Commits []string
	// TotalCommits is the range's commit count, which can exceed len(Commits).
	TotalCommits int
}

// Range compares base...head once and returns the patches of paths and the
// range's commits. Paths not in the compare response are absent from Patches.
func (g GitHubDiffer) Range(ctx context.Context, repo, base, head string, paths []string) (Range, error) {
	cmp, err := g.compare(ctx, repo, base, head)
	if err != nil {
		return Range{}, err
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	r := Range{Patches: map[string]string{}, Commits: make([]string, 0, len(cmp.Commits)), TotalCommits: cmp.GetTotalCommits()}
	for _, c := range cmp.Commits {
		r.Commits = append(r.Commits, c.GetSHA())
	}
	for _, f := range cmp.Files {
		patch := f.GetPatch()
		// A modified binary has changes 0 and no patch.
		if patch == "" && (f.GetChanges() > 0 || f.GetStatus() == "modified") {
			continue
		}
		for _, name := range []string{f.GetFilename(), f.GetPreviousFilename()} {
			if name != "" && want[name] {
				r.Patches[name] = patch
			}
		}
	}
	return r, nil
}

// WarnCommitCap logs when the range has more commits than the compare API
// lists, so PR context covers only the listed commits.
func (r Range) WarnCommitCap(log *slog.Logger, repo string) {
	if r.TotalCommits > len(r.Commits) {
		log.Warn("range has more commits than the compare API lists; PR context covers the listed commits only",
			"repo", repo, "total_commits", r.TotalCommits, "listed_commits", len(r.Commits))
	}
}

// MaxPRBodyBytes caps each pull request body in the source context.
const MaxPRBodyBytes = 4096

// PullRequest is a merged pull request that brought in a commit of a range.
type PullRequest struct {
	Number        int
	Title, Body   string
	BodyTruncated bool
}

// PullRequests lists the merged pull requests that contain any of commits,
// deduplicated by number and sorted by it. Bodies are capped at MaxPRBodyBytes.
// It makes one API call per commit.
func (g GitHubDiffer) PullRequests(ctx context.Context, repo string, commits []string) ([]PullRequest, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	byNumber := map[int]PullRequest{}
	for _, sha := range commits {
		prs, _, err := c.PullRequests.ListPullRequestsWithCommit(ctx, owner, name, sha, &gogithub.ListOptions{PerPage: 100})
		if err != nil {
			return nil, fmt.Errorf("list pull requests for %s@%s: %w", repo, sha, err)
		}
		for _, pr := range prs {
			if pr.MergedAt == nil || byNumber[pr.GetNumber()].Number != 0 {
				continue
			}
			body, truncated := capBody(pr.GetBody())
			byNumber[pr.GetNumber()] = PullRequest{Number: pr.GetNumber(), Title: pr.GetTitle(), Body: body, BodyTruncated: truncated}
		}
	}
	out := make([]PullRequest, 0, len(byNumber))
	for _, pr := range byNumber {
		out = append(out, pr)
	}
	slices.SortFunc(out, func(a, b PullRequest) int { return a.Number - b.Number })
	return out, nil
}

// capBody cuts s to MaxPRBodyBytes at a UTF-8 boundary.
func capBody(s string) (string, bool) {
	if len(s) <= MaxPRBodyBytes {
		return s, false
	}
	cut := MaxPRBodyBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// compare fetches base...head. A 404 with a resolvable head means base is not
// in the repo's history and counts as diverged; a missing head or an
// unreadable repo is an ordinary failure.
func (g GitHubDiffer) compare(ctx context.Context, repo, base, head string) (*gogithub.CommitsComparison, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	cmp, _, err := c.Repositories.CompareCommits(ctx, owner, name, base, head, nil)
	if github.IsNotFound(err) {
		if _, _, herr := c.Repositories.GetCommitSHA1(ctx, owner, name, head, ""); herr != nil {
			return nil, fmt.Errorf("resolve %s@%s: %w", repo, head, herr)
		}
		return nil, fmt.Errorf("%s %s...%s not found: %w", repo, base, head, ErrDiverged)
	}
	if err != nil {
		return nil, fmt.Errorf("compare %s %s...%s: %w", repo, base, head, err)
	}
	switch s := cmp.GetStatus(); s {
	case StatusIdentical, StatusAhead:
	case StatusBehind, StatusDiverged:
		return nil, fmt.Errorf("%s %s...%s is %s: %w", repo, base, head, s, ErrDiverged)
	default:
		return nil, fmt.Errorf("compare %s %s...%s: unknown status %q", repo, base, head, s)
	}
	if len(cmp.Files) >= MaxCompareFiles {
		return nil, fmt.Errorf("%s %s...%s lists %d files: %w", repo, base, head, len(cmp.Files), ErrIncomplete)
	}
	return cmp, nil
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
