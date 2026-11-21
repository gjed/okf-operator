// Package review asks a second model, page by page, whether every changed
// statement of a knowledge PR is supported by the source diff, and encodes
// the verdict as a versioned JSON result that `gate` consumes. It never edits.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"slices"
	"strings"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
)

// Version is the review result JSON contract version.
const Version = 1

// Reasons a page fails without a model verdict.
const (
	ReasonNotInPlan        = "not_in_plan"
	ReasonPageRemoved      = "page_removed"
	ReasonPatchUnavailable = "patch_unavailable"
	ReasonOverBudget       = "over_budget"
	ReasonReplyInvalid     = "reply_invalid"
)

// Reply verdicts.
const (
	VerdictSupported   = "supported"
	VerdictUnsupported = "unsupported"
)

// statusRemoved is the files API status of a deleted file.
const statusRemoved = "removed"

// Result is the JSON document `review` prints. Field order is the output order.
type Result struct {
	Version int    `json:"version"`
	PR      int    `json:"pr"`
	HeadSHA string `json:"head_sha"`
	Model   string `json:"model"`
	Pass    bool   `json:"pass"`
	Pages   []Page `json:"pages"`
	Tokens  Tokens `json:"tokens"`
}

// Page is the verdict for one changed page. Reason is empty when the model
// reviewed the page.
type Page struct {
	Path        string   `json:"path"`
	Pass        bool     `json:"pass"`
	Reason      string   `json:"reason"`
	Unsupported []string `json:"unsupported"`
}

// Tokens counts model tokens.
type Tokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
}

func (t *Tokens) add(o Tokens) {
	t.Prompt += o.Prompt
	t.Completion += o.Completion
}

// Source reads the source repo patches of a range.
type Source interface {
	Range(ctx context.Context, repo, base, head string, paths []string) (plan.Range, error)
}

// Model completes one prompt. *model.Client implements it.
type Model interface {
	Complete(ctx context.Context, r model.Request) (model.Reply, error)
}

// Deps are the inputs Run reads.
type Deps struct {
	Reader           gate.Reader
	Source           Source
	Model            Model
	ModelName        string
	MaxTokensPerPage int
	Logger           *slog.Logger
}

// job is one page on its way to the model.
type job struct {
	page        *Page
	sourcePaths []string
	before      []byte
	after       []byte
	isNew       bool
}

// Run reviews every changed Markdown page of PR n of repo against plan p. An
// error means the review could not finish and there is no result.
func Run(ctx context.Context, d Deps, repo string, n int, p *plan.Plan) (*Result, error) {
	pr, err := d.Reader.PullRequest(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	planned := make(map[string]plan.Page, len(p.Pages))
	for _, pg := range p.Pages {
		planned[pg.Path] = pg
	}

	res := &Result{Version: Version, PR: pr.Number, HeadSHA: pr.HeadSHA, Model: d.ModelName}
	var pages []*Page
	var jobs []*job
	for _, f := range pr.Files {
		if path.Ext(f.Path) != ".md" {
			continue
		}
		pg := &Page{Path: f.Path}
		pages = append(pages, pg)
		pp, ok := planned[f.Path]
		switch {
		case f.Status == statusRemoved:
			pg.Reason = ReasonPageRemoved
		case !ok:
			pg.Reason = ReasonNotInPlan
		default:
			j, err := load(ctx, d.Reader, repo, pr, f, pg)
			if err != nil {
				return nil, err
			}
			j.sourcePaths = slices.Sorted(slices.Values(pp.SourcePaths))
			jobs = append(jobs, j)
		}
	}

	patches, err := fetchPatches(ctx, d, p, jobs)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		tok, err := reviewPage(ctx, d, j, patches)
		res.Tokens.add(tok)
		if err != nil {
			recordSpend(ctx, repo, p, res)
			return nil, fmt.Errorf("page %s: %w", j.page.Path, err)
		}
		d.Logger.InfoContext(ctx, "page reviewed", "page", j.page.Path, "pass", j.page.Pass, "reason", j.page.Reason,
			"unsupported", len(j.page.Unsupported))
	}

	res.Pages = make([]Page, 0, len(pages))
	for _, pg := range pages {
		if pg.Unsupported == nil {
			pg.Unsupported = []string{}
		}
		res.Pages = append(res.Pages, *pg)
	}
	slices.SortFunc(res.Pages, func(a, b Page) int { return strings.Compare(a.Path, b.Path) })
	res.Pass = len(res.Pages) > 0
	for _, pg := range res.Pages {
		res.Pass = res.Pass && pg.Pass
	}
	record(ctx, repo, p, res)
	return res, nil
}

// load reads a page at the base and at the head. A page missing at the base
// is new; a page missing at the head fails the run.
func load(ctx context.Context, r gate.Reader, repo string, pr *gate.PR, f gate.File, pg *Page) (*job, error) {
	after, found, err := r.File(ctx, repo, pr.HeadSHA, f.Path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s is listed as changed but is not a file at head %s", f.Path, pr.HeadSHA)
	}
	basePath := f.Path
	if f.PreviousPath != "" {
		basePath = f.PreviousPath
	}
	before, found, err := r.File(ctx, repo, pr.BaseSHA, basePath)
	if err != nil {
		return nil, err
	}
	return &job{page: pg, before: before, after: after, isNew: !found}, nil
}

// fetchPatches reads the patches of every job's source paths with one compare.
// A range too large to list leaves every patch missing, which fails each page.
func fetchPatches(ctx context.Context, d Deps, p *plan.Plan, jobs []*job) (map[string]string, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	var paths []string
	for _, j := range jobs {
		paths = append(paths, j.sourcePaths...)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	rng, err := d.Source.Range(ctx, p.Source, p.From, p.To, paths)
	if errors.Is(err, plan.ErrIncomplete) {
		d.Logger.WarnContext(ctx, "source range too large to list: every page fails with patch_unavailable", "source", p.Source, "err", err)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rng.Patches, nil
}

// reviewPage sets j.page from one model call, or fails it without a call.
// It returns the tokens spent.
func reviewPage(ctx context.Context, d Deps, j *job, patches map[string]string) (Tokens, error) {
	hunks := make([]hunk, 0, len(j.sourcePaths))
	for _, sp := range j.sourcePaths {
		patch, ok := patches[sp]
		if !ok {
			j.page.Reason = ReasonPatchUnavailable
			return Tokens{}, nil
		}
		hunks = append(hunks, hunk{Path: sp, Patch: patch})
	}
	before, after := align(j.before, j.after, j.isNew)
	prompt, err := render(promptData{Path: j.page.Path, New: j.isNew, Before: string(before), After: string(after), Hunks: hunks})
	if err != nil {
		return Tokens{}, err
	}
	est := estimate(systemPrompt + prompt)
	if est >= d.MaxTokensPerPage {
		j.page.Reason = ReasonOverBudget
		return Tokens{}, nil
	}
	reply, err := d.Model.Complete(ctx, model.Request{System: systemPrompt, User: prompt, MaxTokens: d.MaxTokensPerPage - est})
	if err != nil {
		return Tokens{}, err
	}
	tok := usage(reply, est)
	unsupported, ok := parseReply(reply)
	switch {
	case !ok:
		j.page.Reason = ReasonReplyInvalid
	case len(unsupported) == 0:
		j.page.Pass = true
	default:
		j.page.Unsupported = unsupported
	}
	return tok, nil
}

// align stamps both pages with the head page's provenance, so provenance
// changes never look like changed statements. Pages without a frontmatter
// are returned as they are.
func align(before, after []byte, isNew bool) ([]byte, []byte) {
	f, _, err := provenance.Parse(after)
	if err != nil {
		return before, after
	}
	a, err := provenance.Stamp(after, f)
	if err != nil {
		return before, after
	}
	if isNew {
		return before, a
	}
	b, err := provenance.Stamp(before, f)
	if err != nil {
		return before, after
	}
	return b, a
}

// reply is the JSON object the reviewer must return.
type reply struct {
	Verdict     *string   `json:"verdict"`
	Unsupported *[]string `json:"unsupported"`
}

// parseReply returns the unsupported sentences and whether the reply is
// exactly the expected object with a verdict that matches its list.
func parseReply(r model.Reply) ([]string, bool) {
	if r.FinishReason == "length" {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(unfence(strings.TrimSpace(r.Text))))
	dec.DisallowUnknownFields()
	var rep reply
	if err := dec.Decode(&rep); err != nil || rep.Verdict == nil || rep.Unsupported == nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	var sentences []string
	for _, s := range *rep.Unsupported {
		if s = strings.TrimSpace(s); s != "" {
			sentences = append(sentences, s)
		}
	}
	slices.Sort(sentences)
	sentences = slices.Compact(sentences)
	switch *rep.Verdict {
	case VerdictSupported:
		return nil, len(*rep.Unsupported) == 0
	case VerdictUnsupported:
		return sentences, len(sentences) > 0
	default:
		return nil, false
	}
}

// unfence removes one surrounding Markdown code fence.
func unfence(s string) string {
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") {
		return s
	}
	_, inner, ok := strings.Cut(s, "\n")
	if !ok {
		return s
	}
	return strings.TrimSpace(strings.TrimSuffix(inner, "```"))
}

// usage is the reported usage, or the estimate when the endpoint sent none.
func usage(r model.Reply, promptEst int) Tokens {
	if r.Usage.Reported {
		return Tokens{Prompt: r.Usage.Prompt, Completion: r.Usage.Completion}
	}
	return Tokens{Prompt: promptEst, Completion: estimate(r.Text)}
}

// estimate is a conservative token count for s.
//
// SIMPLIFIED: one token per three bytes, the same upper bound write uses.
// Upgrade path: a tokenizer per model family.
func estimate(s string) int { return (len(s) + 2) / 3 }

// Encode writes r as indented JSON. The same result always encodes to the same bytes.
func Encode(w io.Writer, r *Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// Decode reads one review result v1. Unknown keys and other versions fail.
func Decode(rd io.Reader) (*Result, error) {
	dec := json.NewDecoder(rd)
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode review: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decode review: more than one JSON document")
	}
	if r.Version != Version {
		return nil, fmt.Errorf("review version %d is not supported, want %d", r.Version, Version)
	}
	return &r, nil
}
