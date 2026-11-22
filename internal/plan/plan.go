// Package plan decides which knowledge pages a range of source commits
// affects, without calling a model, and encodes the result as versioned JSON.
package plan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/gjed/okf-operator/internal/routing"
)

// Version is the plan JSON contract version `plan` prints.
const Version = 1

// ReconcileVersion is the plan JSON contract version `reconcile` prints:
// version 1 plus the Reconcile object.
const ReconcileVersion = 2

// Page states.
const (
	StateUpdate  = "update"
	StateCreate  = "create"
	StateMissing = "missing"
)

// Plan is the JSON document `plan` prints. Field order is the output order.
type Plan struct {
	Version      int           `json:"version"`
	Source       string        `json:"source"`
	Bundle       string        `json:"bundle"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	Initial      bool          `json:"initial"`
	ChangedPaths []ChangedPath `json:"changed_paths"`
	Pages        []Page        `json:"pages"`
	Unrouted     []string      `json:"unrouted"`
	Excluded     []string      `json:"excluded"`
	// Reconcile is set in version 2 plans only.
	Reconcile *Reconcile `json:"reconcile,omitempty"`
}

// Cursor states of a reconcile plan, relative to its to.
const (
	CursorMissing  = "missing"
	CursorHead     = "head"
	CursorAncestor = "ancestor"
	CursorDiverged = "diverged"
)

// Reasons a reconcile plan plans a page.
const (
	ReasonBackfill = "backfill"
	ReasonStale    = "stale"
	ReasonRename   = "rename"
)

// Evidence kinds of a reconcile page.
const (
	EvidenceHunks = "hunks"
	EvidenceFiles = "files"
)

// Reconcile is what a version 2 plan adds to version 1.
type Reconcile struct {
	Cursor             string              `json:"cursor"`
	Pages              []ReconcilePage     `json:"pages"`
	DeletionCandidates []DeletionCandidate `json:"deletion_candidates"`
}

// ReconcilePage says why a page is planned and what write sends as evidence:
// the compare patches of Base..to, or the whole source files at to.
type ReconcilePage struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
	Base     string `json:"base"`
}

// DeletionCandidate is a page whose routes and source paths match nothing at
// to. It is reported for a human and never deleted.
type DeletionCandidate struct {
	Path        string   `json:"path"`
	SourceSHA   string   `json:"source_sha"`
	SourcePaths []string `json:"source_paths"`
}

// ReconcilePage returns the reconcile entry of page path, if any.
func (p *Plan) ReconcilePage(path string) (ReconcilePage, bool) {
	if p.Reconcile == nil {
		return ReconcilePage{}, false
	}
	for _, rp := range p.Reconcile.Pages {
		if rp.Path == path {
			return rp, true
		}
	}
	return ReconcilePage{}, false
}

// ChangedPath is one file changed in the range, as the compare API reports it.
type ChangedPath struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	PreviousPath string `json:"previous_path"`
}

// Page is one knowledge page the range affects.
type Page struct {
	Path        string   `json:"path"`
	State       string   `json:"state"`
	Type        string   `json:"type"`
	SourcePaths []string `json:"source_paths"`
}

// Encode writes p as indented JSON. The same plan always encodes to the same bytes.
func Encode(w io.Writer, p *Plan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(p)
}

// Digest identifies p: "sha256:" and the hex SHA-256 of its Encode output.
// Encode is canonical for a decoded plan, so the same plan has the same
// digest however its file was formatted.
func Digest(p *Plan) (string, error) {
	h := sha256.New()
	if err := Encode(h, p); err != nil {
		return "", fmt.Errorf("digest plan: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Errors that require `reconcile`.
var (
	ErrDiverged   = errors.New("cursor diverged from the source history: reconcile required")
	ErrIncomplete = errors.New("range incomplete: the compare API did not return every changed file: reconcile required")
	// ErrBehind accompanies ErrDiverged when head is an ancestor of base.
	ErrBehind = errors.New("the end commit is behind the base")
)

// Compare statuses returned by the GitHub compare API.
const (
	StatusAhead     = "ahead"
	StatusIdentical = "identical"
	StatusBehind    = "behind"
	StatusDiverged  = "diverged"
)

// SourceDiffer lists what changed in a source repo.
type SourceDiffer interface {
	// Head returns the SHA at the head of the repo's default branch.
	Head(ctx context.Context, repo string) (string, error)
	// Changes returns the files changed from base to head. It returns
	// ErrDiverged when base is not an ancestor of head and ErrIncomplete when
	// the list would be partial.
	Changes(ctx context.Context, repo, base, head string) ([]ChangedPath, error)
}

// CursorReader reads the last processed commit of a source repo.
type CursorReader interface {
	Read(ctx context.Context, source string) (sha string, found bool, err error)
}

// PageChecker reports whether a page exists in the knowledge checkout.
type PageChecker interface {
	Exists(page string) (bool, error)
}

// Deps are the inputs Build reads.
type Deps struct {
	Routing *routing.Map
	Cursor  CursorReader
	Differ  SourceDiffer
	Pages   PageChecker
	Logger  *slog.Logger
}

// Build plans the range from the source cursor to to, or to the source
// default branch HEAD when to is empty.
func Build(ctx context.Context, source, to string, d Deps) (*Plan, error) {
	p := &Plan{
		Version:      Version,
		Source:       source,
		To:           to,
		ChangedPaths: []ChangedPath{},
		Pages:        []Page{},
		Unrouted:     []string{},
		Excluded:     []string{},
	}
	src := d.Routing.Source(source)
	if src == nil {
		d.Logger.InfoContext(ctx, "source not onboarded", "source", source)
		record(ctx, p)
		return p, nil
	}
	p.Source, p.Bundle = src.Repo, src.Bundle

	from, found, err := d.Cursor.Read(ctx, src.Repo)
	if err != nil {
		return nil, err
	}
	if p.To == "" {
		if p.To, err = d.Differ.Head(ctx, src.Repo); err != nil {
			return nil, err
		}
	}
	if !found {
		p.Initial = true
		d.Logger.InfoContext(ctx, "no cursor: initial plan anchors at the range end", "source", src.Repo, "to", p.To)
		record(ctx, p)
		return p, nil
	}
	p.From = from
	if from == p.To {
		record(ctx, p)
		return p, nil
	}

	changed, err := d.Differ.Changes(ctx, src.Repo, from, p.To)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(changed, func(a, b ChangedPath) int { return strings.Compare(a.Path, b.Path) })
	p.ChangedPaths = changed

	var paths []string
	for _, c := range changed {
		paths = append(paths, c.Path)
		if c.PreviousPath != "" {
			paths = append(paths, c.PreviousPath)
		}
	}
	res := src.Match(paths)
	p.Unrouted, p.Excluded = res.Unrouted, res.Excluded
	for _, m := range res.Pages {
		state, err := pageState(d.Pages, m)
		if err != nil {
			return nil, err
		}
		p.Pages = append(p.Pages, Page{Path: m.Page, State: state, Type: m.Type, SourcePaths: m.SourcePaths})
	}
	for _, u := range p.Unrouted {
		d.Logger.InfoContext(ctx, "unrouted path", "source", src.Repo, "path", u)
	}
	record(ctx, p)
	return p, nil
}

func pageState(pages PageChecker, m routing.PageMatch) (string, error) {
	ok, err := pages.Exists(m.Page)
	switch {
	case err != nil:
		return "", err
	case ok:
		return StateUpdate, nil
	case m.Type != "":
		return StateCreate, nil
	default:
		return StateMissing, nil
	}
}
