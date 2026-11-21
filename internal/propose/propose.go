// Package propose turns the pages `write` changed into a commit on the bot
// branch of the knowledge repo, opens or updates the one bot PR per source,
// and then advances the source cursor. It never runs git: every write goes
// through the GitHub REST API.
package propose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

// Version is the propose result JSON contract version.
const Version = 1

// Result actions.
const (
	ActionOpened  = "opened"
	ActionUpdated = "updated"
	ActionNone    = "none"
)

// Result is the JSON document `propose` prints. Field order is the output order.
type Result struct {
	Version        int    `json:"version"`
	Source         string `json:"source"`
	Bundle         string `json:"bundle"`
	From           string `json:"from"`
	To             string `json:"to"`
	Action         string `json:"action"`
	PR             *PR    `json:"pr"`
	Commit         string `json:"commit"`
	CursorAdvanced bool   `json:"cursor_advanced"`
}

// PR identifies the bot PR in the result.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Pull is an open bot PR as the knowledge repo reports it.
type Pull struct {
	Number  int
	URL     string
	Body    string
	HeadSHA string
}

// Entry is one blob or symlink of a knowledge repo tree.
type Entry struct {
	Mode string
	SHA  string
}

// Knowledge is the knowledge repo as propose reads and writes it.
type Knowledge interface {
	// DefaultBranch returns the default branch and the commit at its head.
	DefaultBranch(ctx context.Context) (branch, head string, err error)
	// OpenPull returns the open PR from head into base, or nil.
	OpenPull(ctx context.Context, head, base string) (*Pull, error)
	// Tree lists every blob and symlink of commit by path. Submodules are
	// listed with mode 160000. A truncated listing is an error.
	Tree(ctx context.Context, commit string) (map[string]Entry, error)
	// Commit creates a commit with parent base whose tree is base's tree with
	// files replaced, and returns its SHA.
	Commit(ctx context.Context, base string, files map[string]File, message string) (string, error)
	// MoveBranch points branch at sha. With fastForward it refuses anything
	// but a fast-forward; otherwise it creates the branch or resets it.
	MoveBranch(ctx context.Context, branch, sha string, fastForward bool) error
	// OpenPR opens a PR from head into base.
	OpenPR(ctx context.Context, title, head, base, body string) (*Pull, error)
	// EditPR replaces the body of PR number.
	EditPR(ctx context.Context, number int, body string) (*Pull, error)
}

// File is the new content and git mode of one committed page.
type File struct {
	Mode    string
	Content []byte
}

// SourcePRs lists the merged source PRs behind a commit range.
type SourcePRs interface {
	// PullRequests returns the merged PR numbers that brought in commits of
	// from..to, the range's commit count and how many commits were listed.
	PullRequests(ctx context.Context, repo, from, to string) (numbers []int, total, listed int, err error)
}

// Cursor checks and advances the source cursor on kb-state.
type Cursor interface {
	Check(ctx context.Context, source, from string, initial bool) error
	Advance(ctx context.Context, source, from, to string, initial bool) error
}

// Deps are the inputs Run uses.
type Deps struct {
	Knowledge     Knowledge
	SourcePRs     SourcePRs
	Cursor        Cursor
	KnowledgeRepo string // owner/name
	KnowledgeDir  string
	Base          string // commit KnowledgeDir is checked out at
	Logger        *slog.Logger
}

// BranchPrefix starts every bot branch name: okf/<owner>/<name>.
const BranchPrefix = "okf/"

// Branch is the bot branch of source owner/name.
func Branch(source string) string { return BranchPrefix + source }

// DecodeReport reads one write report document of version write.Version.
// Unknown keys and other versions fail.
func DecodeReport(r io.Reader) (*write.Report, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var rep write.Report
	if err := dec.Decode(&rep); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decode report: more than one JSON document")
	}
	if rep.Version != write.Version {
		return nil, fmt.Errorf("report version %d is not supported, want %d", rep.Version, write.Version)
	}
	return &rep, nil
}

// Agree checks that rep is the write report of p: same range and the same
// set of page paths.
func Agree(p *plan.Plan, rep *write.Report) error {
	for _, f := range []struct{ name, plan, report string }{
		{"source", p.Source, rep.Source},
		{"bundle", p.Bundle, rep.Bundle},
		{"from", p.From, rep.From},
		{"to", p.To, rep.To},
	} {
		if f.plan != f.report {
			return fmt.Errorf("plan and report disagree on %s: plan %q, report %q", f.name, f.plan, f.report)
		}
	}
	planned := make(map[string]bool, len(p.Pages))
	for _, pg := range p.Pages {
		planned[pg.Path] = true
	}
	reported := make(map[string]bool, len(rep.Pages))
	for _, pg := range rep.Pages {
		if !planned[pg.Path] {
			return fmt.Errorf("report page %s is not a plan page", pg.Path)
		}
		if reported[pg.Path] {
			return fmt.Errorf("report lists page %s twice", pg.Path)
		}
		switch pg.Result {
		case write.ResultChanged, write.ResultCreated, write.ResultUnchanged, write.ResultDropped:
		default:
			return fmt.Errorf("report page %s has unknown result %q", pg.Path, pg.Result)
		}
		reported[pg.Path] = true
	}
	for _, pg := range p.Pages {
		if !reported[pg.Path] {
			return fmt.Errorf("plan page %s is missing from the report", pg.Path)
		}
	}
	return nil
}

// Run proposes the pages rep marks changed or created and advances the cursor. All
// reads happen before the first write, and the cursor moves last.
func Run(ctx context.Context, p *plan.Plan, rep *write.Report, d Deps) (*Result, error) {
	if err := Agree(p, rep); err != nil {
		return nil, err
	}
	res := &Result{Version: Version, Source: p.Source, Bundle: p.Bundle, From: p.From, To: p.To, Action: ActionNone}
	if !p.Initial && p.From == p.To {
		d.Logger.InfoContext(ctx, "empty range: nothing to propose", "source", p.Source)
		record(ctx, res, 0)
		return res, nil
	}
	if err := d.Cursor.Check(ctx, p.Source, p.From, p.Initial); err != nil {
		return nil, err
	}
	if p.Initial {
		// An initial plan has no pages (Agree checked the report matches): it
		// only anchors the cursor and never touches the knowledge branches.
		if err := d.Cursor.Advance(ctx, p.Source, p.From, p.To, true); err != nil {
			return nil, err
		}
		res.CursorAdvanced = true
		d.Logger.InfoContext(ctx, "initial plan: cursor anchored without a PR", "source", p.Source, "to", p.To)
		record(ctx, res, 0)
		return res, nil
	}

	branch := Branch(p.Source)
	defBranch, defHead, err := d.Knowledge.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(d.KnowledgeRepo, "/")
	pull, err := d.Knowledge.OpenPull(ctx, owner+":"+branch, defBranch)
	if err != nil {
		return nil, err
	}
	if pull != nil {
		res.PR = &PR{Number: pull.Number, URL: pull.URL}
		if d.Base != pull.HeadSHA {
			return nil, fmt.Errorf("bot PR #%d is open: check out %s at %s and re-run write, not %s", pull.Number, branch, pull.HeadSHA, d.Base)
		}
	} else if d.Base != defHead {
		return nil, fmt.Errorf("no bot PR is open: check out %s at %s and re-run write, not %s", defBranch, defHead, d.Base)
	}

	tree, err := d.Knowledge.Tree(ctx, d.Base)
	if err != nil {
		return nil, err
	}
	files, err := Verify(d.KnowledgeDir, tree, pagesWith(rep, write.ResultChanged), pagesWith(rep, write.ResultCreated))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		if err := d.Cursor.Advance(ctx, p.Source, p.From, p.To, false); err != nil {
			return nil, err
		}
		res.CursorAdvanced = true
		d.Logger.InfoContext(ctx, "no page changed: cursor advanced without a PR", "source", p.Source, "to", p.To)
		record(ctx, res, 0)
		return res, nil
	}

	numbers, total, listed, err := d.SourcePRs.PullRequests(ctx, p.Source, p.From, p.To)
	if err != nil {
		return nil, err
	}
	sec := section(p, rep, numbers, total, listed)

	commit, err := d.Knowledge.Commit(ctx, d.Base, files, commitMessage(p, numbers))
	if err != nil {
		return nil, err
	}
	if err := d.Knowledge.MoveBranch(ctx, branch, commit, pull != nil); err != nil {
		return nil, err
	}
	res.Commit = commit
	if pull == nil {
		if pull, err = d.Knowledge.OpenPR(ctx, title(p), branch, defBranch, newBody(p, sec)); err != nil {
			return nil, err
		}
		res.Action = ActionOpened
	} else {
		if pull, err = d.Knowledge.EditPR(ctx, pull.Number, appendSection(pull.Body, sec)); err != nil {
			return nil, err
		}
		res.Action = ActionUpdated
	}
	res.PR = &PR{Number: pull.Number, URL: pull.URL}
	d.Logger.InfoContext(ctx, "knowledge PR "+res.Action, "source", p.Source, "pr", pull.Number, "commit", commit, "pages", len(files))

	if err := d.Cursor.Advance(ctx, p.Source, p.From, p.To, false); err != nil {
		record(ctx, res, len(files))
		return nil, err
	}
	res.CursorAdvanced = true
	record(ctx, res, len(files))
	return res, nil
}

func pagesWith(rep *write.Report, result string) map[string]bool {
	out := map[string]bool{}
	for _, pg := range rep.Pages {
		if pg.Result == result {
			out[pg.Path] = true
		}
	}
	return out
}

func title(p *plan.Plan) string {
	return fmt.Sprintf("okf: update %s from %s", p.Bundle, p.Source)
}

func commitMessage(p *plan.Plan, numbers []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "okf: update %s from %s %s..%s\n", p.Bundle, p.Source, short(p.From), short(p.To))
	if len(numbers) > 0 {
		refs := make([]string, 0, len(numbers))
		for _, n := range slices.Sorted(slices.Values(numbers)) {
			refs = append(refs, fmt.Sprintf("%s#%d", p.Source, n))
		}
		fmt.Fprintf(&b, "\nSource PRs: %s\n", strings.Join(refs, ", "))
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
