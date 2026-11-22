// Package reconcile plans every routed knowledge page of one source repo
// against the source tree at one commit, without calling a model, and encodes
// the result as a plan of version 2 that write and propose consume.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
	"github.com/gjed/okf-operator/internal/routing"
)

// Errors that stop a reconcile before any plan.
var (
	ErrNotOnboarded = errors.New("source is not in the routing map")
	ErrTruncated    = errors.New("source tree listing is truncated: a partial tree would report routed pages as gone")
	ErrRollback     = errors.New("reconcile never moves the cursor or a page back to an older commit: pass a later --to")
)

// TreeLister lists a source repo's files.
type TreeLister interface {
	// Paths returns every blob path of repo at ref. It returns ErrTruncated
	// when the listing is incomplete.
	Paths(ctx context.Context, repo, ref string) ([]string, error)
}

// Deps are the inputs Build reads.
type Deps struct {
	Routing      *routing.Map
	Cursor       plan.CursorReader
	Differ       plan.SourceDiffer
	Tree         TreeLister
	KnowledgeDir string
	Logger       *slog.Logger
}

// reserved are OKF filenames that are never concept pages.
var reserved = map[string]bool{"index.md": true, "log.md": true}

// page is one knowledge page reconcile considers.
type page struct {
	exists  bool
	hasProv bool
	prov    provenance.Fields
}

// comparison is the result of one compare call, cached per base.
type comparison struct {
	changed []plan.ChangedPath
	err     error
}

// Build reconciles source at to, or at the source default branch HEAD when to
// is empty.
func Build(ctx context.Context, source, to string, d Deps) (*plan.Plan, error) {
	src := d.Routing.Source(source)
	if src == nil {
		return nil, fmt.Errorf("%s: %w", source, ErrNotOnboarded)
	}
	p := &plan.Plan{
		Version:      plan.ReconcileVersion,
		Source:       src.Repo,
		Bundle:       src.Bundle,
		To:           to,
		ChangedPaths: []plan.ChangedPath{},
		Pages:        []plan.Page{},
		Unrouted:     []string{},
		Excluded:     []string{},
		Reconcile:    &plan.Reconcile{Pages: []plan.ReconcilePage{}, DeletionCandidates: []plan.DeletionCandidate{}},
	}
	from, found, err := d.Cursor.Read(ctx, src.Repo)
	if err != nil {
		return nil, err
	}
	if p.To == "" {
		if p.To, err = d.Differ.Head(ctx, src.Repo); err != nil {
			return nil, err
		}
	}
	tree, err := d.Tree.Paths(ctx, src.Repo, p.To)
	if err != nil {
		return nil, err
	}
	atTo := make(map[string]bool, len(tree))
	for _, t := range tree {
		atTo[t] = true
	}
	routed := map[string]routing.PageMatch{}
	for _, m := range src.Match(tree).Pages {
		routed[m.Page] = m
	}

	cache := map[string]comparison{}
	compare := func(base string) comparison {
		c, ok := cache[base]
		if !ok {
			changed, err := d.Differ.Changes(ctx, src.Repo, base, p.To)
			c = comparison{changed: changed, err: err}
			cache[base] = c
		}
		return c
	}

	p.From, p.Initial = from, !found
	switch {
	case !found:
		p.Reconcile.Cursor = plan.CursorMissing
	case from == p.To:
		p.Reconcile.Cursor = plan.CursorHead
	default:
		switch c := compare(from); {
		case errors.Is(c.err, plan.ErrBehind):
			return nil, fmt.Errorf("cursor %s is ahead of %s: %w", from, p.To, ErrRollback)
		case errors.Is(c.err, plan.ErrDiverged):
			p.Reconcile.Cursor = plan.CursorDiverged
			d.Logger.WarnContext(ctx, "cursor diverged: propose re-anchors it at the reconciled commit", "source", src.Repo, "cursor", from, "to", p.To)
		case c.err == nil || errors.Is(c.err, plan.ErrIncomplete):
			p.Reconcile.Cursor = plan.CursorAncestor
		default:
			return nil, c.err
		}
	}

	pages, err := readPages(d, src)
	if err != nil {
		return nil, err
	}
	for _, path := range slices.Sorted(maps.Keys(pages)) {
		pg := pages[path]
		m := routed[path]
		if len(m.SourcePaths) == 0 {
			if pg.exists && noneExist(pg.prov.SourcePaths, atTo) {
				p.Reconcile.DeletionCandidates = append(p.Reconcile.DeletionCandidates, plan.DeletionCandidate{
					Path: path, SourceSHA: pg.prov.SourceSHA, SourcePaths: sorted(pg.prov.SourcePaths),
				})
				d.Logger.WarnContext(ctx, "deletion candidate: no routed source path is left", "source", src.Repo, "page", path)
			} else if pg.exists {
				d.Logger.InfoContext(ctx, "page has no routed path at the reconciled commit but its source paths remain", "source", src.Repo, "page", path)
			}
			continue
		}
		entry, err := selectPage(path, pg, m, src, p.To, compare)
		if err != nil {
			return nil, err
		}
		if entry == nil {
			continue
		}
		p.Pages = append(p.Pages, entry.page)
		p.Reconcile.Pages = append(p.Reconcile.Pages, entry.rp)
		d.Logger.InfoContext(ctx, "page planned", "source", src.Repo, "page", path, "reason", entry.rp.Reason, "evidence", entry.rp.Evidence)
	}
	record(ctx, p)
	return p, nil
}

type planned struct {
	page plan.Page
	rp   plan.ReconcilePage
}

// selectPage decides whether a page with routed paths m is planned, and how.
// It returns nil for a page that is current at to.
func selectPage(path string, pg page, m routing.PageMatch, src *routing.Source, to string, compare func(string) comparison) (*planned, error) {
	entry := func(reason, evidence, base string) plan.ReconcilePage {
		return plan.ReconcilePage{Path: path, Reason: reason, Evidence: evidence, Base: base, RoutedPaths: slices.Clone(m.SourcePaths)}
	}
	whole := func(state, reason string) *planned {
		return &planned{
			page: plan.Page{Path: path, State: state, Type: m.Type, SourcePaths: m.SourcePaths},
			rp:   entry(reason, plan.EvidenceFiles, ""),
		}
	}
	// added lists the routed paths at to that the page's recorded routed set
	// lacks and that are not in changed. It is empty when the set is unknown.
	added := func(changed map[string]bool) []string {
		if pg.prov.RoutedPaths == nil {
			return nil
		}
		var out []string
		for _, sp := range m.SourcePaths {
			if !changed[sp] && !slices.Contains(pg.prov.RoutedPaths, sp) {
				out = append(out, sp)
			}
		}
		return out
	}
	// widened plans the routed paths that are new or in changed as whole files.
	widened := func(changed map[string]bool, added []string) *planned {
		var sources []string
		for _, sp := range m.SourcePaths {
			if changed[sp] || slices.Contains(added, sp) {
				sources = append(sources, sp)
			}
		}
		return &planned{
			page: plan.Page{Path: path, State: plan.StateUpdate, Type: m.Type, SourcePaths: sources},
			rp:   entry(plan.ReasonWidened, plan.EvidenceFiles, ""),
		}
	}
	if !pg.exists {
		if m.Type != "" {
			return whole(plan.StateCreate, plan.ReasonBackfill), nil
		}
		return whole(plan.StateMissing, plan.ReasonBackfill), nil
	}
	base := pg.prov.SourceSHA
	if !pg.hasProv || !strings.EqualFold(pg.prov.SourceRepo, src.Repo) || !cursor.ValidSHA(base) {
		return whole(plan.StateUpdate, plan.ReasonBackfill), nil
	}
	if base == to {
		if a := added(nil); len(a) > 0 {
			return widened(nil, a), nil
		}
		return nil, nil
	}
	c := compare(base)
	switch {
	case errors.Is(c.err, plan.ErrBehind):
		return nil, fmt.Errorf("page %s: source_sha %s is ahead of %s: %w", path, base, to, ErrRollback)
	case errors.Is(c.err, plan.ErrDiverged):
		return whole(plan.StateUpdate, plan.ReasonBackfill), nil
	case errors.Is(c.err, plan.ErrIncomplete):
		return whole(plan.StateUpdate, plan.ReasonStale), nil
	case c.err != nil:
		return nil, c.err
	}
	var paths []string
	renamedFrom := map[string]bool{}
	changed := map[string]bool{}
	for _, ch := range c.changed {
		paths = append(paths, ch.Path)
		changed[ch.Path] = true
		if ch.PreviousPath != "" {
			paths = append(paths, ch.PreviousPath)
			renamedFrom[ch.PreviousPath] = true
		}
	}
	var sources []string
	for _, cm := range src.Match(paths).Pages {
		if cm.Page == path {
			sources = cm.SourcePaths
		}
	}
	// A routed path absent at to was removed or renamed away: only hunks show
	// that to the model, so a widened route waits for the next run.
	gone := slices.ContainsFunc(sources, func(sp string) bool { return !slices.Contains(m.SourcePaths, sp) })
	newPaths := added(changed)
	if len(newPaths) > 0 && !gone {
		return widened(changed, newPaths), nil
	}
	if len(sources) == 0 {
		return nil, nil
	}
	reason := plan.ReasonStale
	for _, sp := range pg.prov.SourcePaths {
		if renamedFrom[sp] {
			reason = plan.ReasonRename
		}
	}
	rp := entry(reason, plan.EvidenceHunks, base)
	// The new paths are left out of the routed set the page records, so the
	// next run plans them as widened.
	rp.RoutedPaths = slices.DeleteFunc(rp.RoutedPaths, func(sp string) bool { return slices.Contains(newPaths, sp) })
	return &planned{
		page: plan.Page{Path: path, State: plan.StateUpdate, Type: m.Type, SourcePaths: sources},
		rp:   rp,
	}, nil
}

// readPages reads every route target of src and every bundle page whose
// provenance names src. A malformed route target fails; other malformed
// pages are skipped.
func readPages(d Deps, src *routing.Source) (map[string]page, error) {
	root, err := os.OpenRoot(d.KnowledgeDir)
	if err != nil {
		return nil, fmt.Errorf("open knowledge dir: %w", err)
	}
	defer func() { _ = root.Close() }()

	pages := map[string]page{}
	for _, r := range src.Routes {
		pages[r.Page] = page{}
	}
	for path, pg := range pages {
		b, err := root.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read page %s: %w", path, err)
		}
		pg.exists = true
		f, _, err := provenance.Parse(b)
		switch {
		case errors.Is(err, provenance.ErrNoFrontmatter):
		case err != nil:
			return nil, fmt.Errorf("page %s: %w", path, err)
		default:
			pg.hasProv, pg.prov = true, f
		}
		pages[path] = pg
	}

	err = fs.WalkDir(root.FS(), src.Bundle, func(path string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir() && e.Name() == ".kb":
			return fs.SkipDir
		case !e.Type().IsRegular() || !strings.HasSuffix(path, ".md") || reserved[e.Name()]:
			return nil
		}
		if _, ok := pages[path]; ok {
			return nil
		}
		b, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		f, _, err := provenance.Parse(b)
		if err != nil {
			if !errors.Is(err, provenance.ErrNoFrontmatter) {
				d.Logger.Info("page skipped: malformed frontmatter", "page", path, "error", err.Error())
			}
			return nil
		}
		if strings.EqualFold(f.SourceRepo, src.Repo) {
			pages[path] = page{exists: true, hasProv: true, prov: f}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read bundle %s: %w", src.Bundle, err)
	}
	return pages, nil
}

func noneExist(paths []string, at map[string]bool) bool {
	for _, p := range paths {
		if at[p] {
			return false
		}
	}
	return true
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	slices.Sort(out)
	return out
}
