package propose

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

// Regenerated is what a RegenerateFunc wrote: the plan it ran write for
// (the conflicting pages only), the plan of the whole range, the write report
// and the checkout of the default branch head that write changed.
type Regenerated struct {
	Plan   *plan.Plan
	Full   *plan.Plan
	Report *write.Report
	Dir    string
}

// RegenerateFunc runs plan and write for the conflicting pages over the
// source range from..to, in a checkout of the default branch head, so the
// page the model updates is the default branch's version.
type RegenerateFunc func(ctx context.Context, conflicts []string, from, to string) (*Regenerated, error)

// Repairer is what Repair needs of the knowledge repo.
type Repairer interface {
	Tree(ctx context.Context, commit string) (map[string]Entry, error)
	MergeBase(ctx context.Context, a, b string) (string, error)
	CommitMerge(ctx context.Context, parents []string, reuse map[string]*Entry, files map[string]File, message string) (string, error)
	MoveBranch(ctx context.Context, branch, sha string, fastForward bool) error
	EditPR(ctx context.Context, number int, body string) (*Pull, error)
}

// RepairInput is what Repair works on: the open bot PR as the run read it
// (Pull.HeadSHA is the head H) and the default branch head D.
type RepairInput struct {
	Knowledge          Repairer
	Source             string
	Pull               *Pull
	DefBranch, DefHead string
	Regenerate         RegenerateFunc
	Logger             *slog.Logger
}

// Repaired is the outcome of Repair.
type Repaired struct {
	// Head is the new head of the bot branch: a commit whose parents are the
	// default branch head and the old PR head.
	Head string
	// Conflicts are the pages both branches changed, regenerated or not.
	Conflicts []string
	// Plan is the plan of the PR's whole range, nil when no page conflicted.
	Plan *plan.Plan
}

// Repair brings a conflicting bot PR back to a branch that merges. It finds
// the pages both the bot branch and the default branch changed since their
// merge base from the git trees, regenerates only those with in.Regenerate
// over the PR's whole source range, and pushes one commit with parents D and
// H, so the PR keeps its number and history. Bot changes to other pages and
// default branch changes to other pages are kept as they are. A conflicting
// page that write does not regenerate keeps the default branch's version.
//
// It reads no cursor and writes none. Everything it can refuse (the range of
// the PR body, the plan's pages) is checked before the first write, and the
// branch only moves as a fast-forward of H, so a push that landed meanwhile
// fails the repair.
func Repair(ctx context.Context, in RepairInput) (*Repaired, error) {
	pull := in.Pull
	from, to, err := PRRange(pull.Body)
	if err != nil {
		return nil, fmt.Errorf("bot PR #%d cannot be regenerated: %w", pull.Number, err)
	}
	mergeBase, err := in.Knowledge.MergeBase(ctx, in.DefHead, pull.HeadSHA)
	if err != nil {
		return nil, err
	}
	baseTree, defTree, headTree, err := trees(ctx, in.Knowledge, mergeBase, in.DefHead, pull.HeadSHA)
	if err != nil {
		return nil, err
	}
	conflicts, take := diffTrees(baseTree, defTree, headTree)
	out := &Repaired{Conflicts: conflicts}

	files := map[string]File{}
	var rep *write.Report
	if len(conflicts) > 0 {
		rg, err := in.Regenerate(ctx, conflicts, from, to)
		if err != nil {
			return nil, err
		}
		if files, err = regeneratedFiles(rg, conflicts, defTree); err != nil {
			return nil, fmt.Errorf("bot PR #%d: %w", pull.Number, err)
		}
		rep, out.Plan = rg.Report, rg.Full
	}

	msg := repairMessage(in.DefBranch, Branch(in.Source), from, to, conflicts)
	if out.Head, err = in.Knowledge.CommitMerge(ctx, []string{in.DefHead, pull.HeadSHA}, take, files, msg); err != nil {
		return nil, err
	}
	if err := in.Knowledge.MoveBranch(ctx, Branch(in.Source), out.Head, true); err != nil {
		return nil, err
	}
	repaired(ctx, in.Source, RepairRegenerate)
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("okf.propose.repair.conflicts", len(conflicts)))
	in.Logger.InfoContext(ctx, "bot PR repaired: default branch merged in", "source", in.Source, "pr", pull.Number,
		"commit", out.Head, "conflicts", len(conflicts), "regenerated", len(files))
	if _, err := in.Knowledge.EditPR(ctx, pull.Number, appendSection(pull.Body, repairNote(in.DefBranch, in.DefHead, from, to, conflicts, rep))); err != nil {
		return nil, err
	}
	return out, nil
}

func trees(ctx context.Context, k Repairer, base, def, head string) (b, d, h map[string]Entry, err error) {
	for _, t := range []struct {
		commit string
		into   *map[string]Entry
	}{{base, &b}, {def, &d}, {head, &h}} {
		if *t.into, err = k.Tree(ctx, t.commit); err != nil {
			return nil, nil, nil, err
		}
	}
	return b, d, h, nil
}

// diffTrees compares the default branch tree def and the bot branch tree head
// against their merge base tree. A path conflicts when both sides changed it
// and now differ; both sides making the same change is not a conflict. take
// holds, for every other path only the bot branch changed, its entry at head,
// or nil when the bot branch deleted it: the changes to lay on def's tree.
func diffTrees(base, def, head map[string]Entry) (conflicts []string, take map[string]*Entry) {
	changed := func(side map[string]Entry, p string) bool {
		b, inBase := base[p]
		s, inSide := side[p]
		return inBase != inSide || b != s
	}
	take = map[string]*Entry{}
	paths := map[string]bool{}
	for _, t := range []map[string]Entry{base, head} {
		for p := range t {
			paths[p] = true
		}
	}
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		if !changed(head, p) {
			continue
		}
		h, inHead := head[p]
		if changed(def, p) {
			if d, inDef := def[p]; inDef != inHead || d != h {
				conflicts = append(conflicts, p)
			}
			continue
		}
		if inHead {
			take[p] = &h
		} else {
			take[p] = nil
		}
	}
	return conflicts, take
}

// regeneratedFiles checks that rg is the regeneration of exactly the
// conflicting pages and returns the content to commit for each page write
// changed or created, verified against the default branch tree.
func regeneratedFiles(rg *Regenerated, conflicts []string, def map[string]Entry) (map[string]File, error) {
	var planned []string
	for _, pg := range rg.Plan.Pages {
		planned = append(planned, pg.Path)
	}
	slices.Sort(planned)
	if !slices.Equal(planned, conflicts) {
		return nil, fmt.Errorf("the regeneration plan has pages %v, the conflicting pages are %v", planned, conflicts)
	}
	if err := Agree(rg.Plan, rg.Report); err != nil {
		return nil, err
	}
	return Verify(rg.Dir, def, pagesWith(rg.Report, write.ResultChanged), pagesWith(rg.Report, write.ResultCreated))
}

func repairMessage(defBranch, branch, from, to string, conflicts []string) string {
	msg := fmt.Sprintf("okf: merge %s into %s\n", defBranch, branch)
	if len(conflicts) > 0 {
		msg += fmt.Sprintf("\nPages regenerated over %s..%s because both branches changed them: %s\n", short(from), short(to), strings.Join(conflicts, ", "))
	}
	return msg
}

// repairNote is the PR body note of a repair. It has no range marker, so it
// is never read as a source range.
func repairNote(defBranch, defHead, from, to string, conflicts []string, rep *write.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Conflict repair\n\nMerged `%s` at `%s` into the branch.\n", defBranch, short(defHead))
	if len(conflicts) == 0 {
		b.WriteString("\nNo page was changed on both branches.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nPages changed on both branches, regenerated over `%s..%s` from the %s version of each:\n\n", short(from), short(to), defBranch)
	results := map[string]write.PageResult{}
	for _, pg := range rep.Pages {
		results[pg.Path] = pg
	}
	for _, p := range conflicts {
		pg := results[p]
		fmt.Fprintf(&b, "- %s: `%s`", pg.Result, p)
		switch {
		case pg.Result == write.ResultChanged || pg.Result == write.ResultCreated:
		case pg.Reason != "":
			fmt.Fprintf(&b, " (%s; the %s version was kept)", pg.Reason, defBranch)
		default:
			fmt.Fprintf(&b, " (the %s version was kept)", defBranch)
		}
		b.WriteString("\n")
	}
	return b.String()
}
