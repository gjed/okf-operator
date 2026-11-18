// Package write turns a plan into updated knowledge pages: one model call per
// planned page, validated, stamped with provenance and written in place.
package write

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
)

// Version is the write report JSON contract version.
const Version = 1

// Page results.
const (
	ResultChanged   = "changed"
	ResultUnchanged = "unchanged"
	ResultDropped   = "dropped"
)

// Drop reasons.
const (
	ReasonPatchUnavailable  = "patch_unavailable"
	ReasonValidationFailed  = "validation_failed"
	ReasonCreateUnsupported = "create_unsupported"
	ReasonPageMissing       = "page_missing"
)

// NoChange is the reply that tells write the page is still accurate.
const NoChange = "NO_CHANGE"

// promptMarker matches the block marker lines of prompts/page.md. A reply
// that contains one echoed the prompt instead of returning the page.
var promptMarker = regexp.MustCompile(`(?m)^<<<(END-)?(WRITING-GUIDANCE|PAGE-TEMPLATE|REFERENCE-PAGE|CURRENT-PAGE|UNTRUSTED-SOURCE-DATA)\b[^\n]*`)

// Report is the JSON document `write` prints. Field order is the output order.
type Report struct {
	Version     int          `json:"version"`
	Source      string       `json:"source"`
	Bundle      string       `json:"bundle"`
	From        string       `json:"from"`
	To          string       `json:"to"`
	Model       string       `json:"model"`
	GeneratedBy string       `json:"generated_by"`
	Pages       []PageResult `json:"pages"`
	Tokens      Tokens       `json:"tokens"`
}

// PageResult is the outcome for one plan page.
type PageResult struct {
	Path     string `json:"path"`
	Result   string `json:"result"`
	Reason   string `json:"reason"`
	Attempts int    `json:"attempts"`
	Tokens   Tokens `json:"tokens"`
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

func (t Tokens) total() int { return t.Prompt + t.Completion }

// Source reads the source repository data a run needs.
type Source interface {
	Range(ctx context.Context, repo, base, head string, paths []string) (plan.Range, error)
	PullRequests(ctx context.Context, repo string, commits []string) ([]plan.PullRequest, error)
}

// Model completes one prompt. *model.Client implements it.
type Model interface {
	Complete(ctx context.Context, r model.Request) (model.Reply, error)
}

// Validator checks one page. A non-nil error is shown to the model on retry.
type Validator interface {
	Validate(path string, page []byte) error
}

// Deps are the inputs Run reads.
type Deps struct {
	Source       Source
	Model        Model
	Validator    Validator
	KnowledgeDir string
	// ModelName and ToolVersion form generated_by: okf-operator/<ToolVersion>/<ModelName>.
	ModelName        string
	ToolVersion      string
	MaxPages         int
	MaxTokensPerPage int
	Now              func() time.Time
	Logger           *slog.Logger
}

// DecodePlan reads one plan v1 document. Unknown keys and other versions fail.
func DecodePlan(r io.Reader) (*plan.Plan, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var p plan.Plan
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decode plan: more than one JSON document")
	}
	if p.Version != plan.Version {
		return nil, fmt.Errorf("plan version %d is not supported, want %d", p.Version, plan.Version)
	}
	return &p, nil
}

// Encode writes r as indented JSON. The same report always encodes to the same bytes.
func Encode(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// job is one update page on its way through the model.
type job struct {
	page    plan.Page
	current []byte
	data    promptData
	prompt  string
	result  PageResult
	output  []byte
}

// Run processes every page of p. Files under d.KnowledgeDir change only when
// Run returns a nil error, and then only the pages reported as changed.
func Run(ctx context.Context, p *plan.Plan, d Deps) (*Report, error) {
	rep := &Report{
		Version:     Version,
		Source:      p.Source,
		Bundle:      p.Bundle,
		From:        p.From,
		To:          p.To,
		Model:       d.ModelName,
		GeneratedBy: "okf-operator/" + d.ToolVersion + "/" + d.ModelName,
		Pages:       []PageResult{},
	}
	planned := make(map[string]bool, len(p.Pages))
	for _, pg := range p.Pages {
		if err := checkPagePath(p.Bundle, pg.Path); err != nil {
			return nil, err
		}
		planned[pg.Path] = true
	}

	var jobs []*job
	for _, pg := range p.Pages {
		switch pg.State {
		case plan.StateUpdate:
			jobs = append(jobs, &job{page: pg, result: PageResult{Path: pg.Path}})
		case plan.StateCreate:
			rep.Pages = append(rep.Pages, PageResult{Path: pg.Path, Result: ResultDropped, Reason: ReasonCreateUnsupported})
		case plan.StateMissing:
			rep.Pages = append(rep.Pages, PageResult{Path: pg.Path, Result: ResultDropped, Reason: ReasonPageMissing})
		default:
			return nil, fmt.Errorf("page %s: unknown state %q", pg.Path, pg.State)
		}
	}
	if len(jobs) > d.MaxPages {
		return nil, fmt.Errorf("plan has %d update pages, over --max-pages %d: no model call made", len(jobs), d.MaxPages)
	}
	if len(jobs) == 0 {
		slices.SortFunc(rep.Pages, func(a, b PageResult) int { return strings.Compare(a.Path, b.Path) })
		record(ctx, rep, 0)
		return rep, nil
	}

	root, err := os.OpenRoot(d.KnowledgeDir)
	if err != nil {
		return nil, fmt.Errorf("open knowledge dir: %w", err)
	}
	defer func() { _ = root.Close() }()
	kb, err := loadKnowledge(root, p.Bundle, planned)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.current, err = root.ReadFile(j.page.Path); err != nil {
			return nil, fmt.Errorf("read page %s: %w", j.page.Path, err)
		}
	}

	src, err := fetchSource(ctx, p, jobs, d)
	if err != nil {
		return nil, err
	}
	if err := prepare(jobs, kb, src, d.MaxTokensPerPage); err != nil {
		return nil, err
	}

	fields := provenance.Fields{
		SourceRepo:  p.Source,
		SourceSHA:   p.To,
		UpdatedAt:   d.Now().UTC().Truncate(time.Second),
		GeneratedBy: rep.GeneratedBy,
	}
	failures := 0
	for _, j := range jobs {
		if j.result.Result != "" {
			continue // dropped while preparing
		}
		f := fields
		f.SourcePaths = slices.Sorted(slices.Values(j.page.SourcePaths))
		n, err := process(ctx, j, f, d)
		failures += n
		if err != nil {
			recordAborted(ctx, rep, jobs, failures)
			return nil, err
		}
		d.Logger.InfoContext(ctx, "page processed", "page", j.page.Path, "result", j.result.Result, "reason", j.result.Reason, "attempts", j.result.Attempts)
	}

	if err := writePages(root, jobs); err != nil {
		recordAborted(ctx, rep, jobs, failures)
		return nil, err
	}
	for _, j := range jobs {
		rep.Pages = append(rep.Pages, j.result)
		rep.Tokens.add(j.result.Tokens)
	}
	slices.SortFunc(rep.Pages, func(a, b PageResult) int { return strings.Compare(a.Path, b.Path) })
	record(ctx, rep, failures)
	return rep, nil
}

// sourceData is what the source repo contributes to every prompt of a run.
type sourceData struct {
	patches map[string]string
	prs     []plan.PullRequest
	omitted int // older PRs left out by capPullRequests
}

func fetchSource(ctx context.Context, p *plan.Plan, jobs []*job, d Deps) (sourceData, error) {
	var paths []string
	for _, j := range jobs {
		paths = append(paths, j.page.SourcePaths...)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	rng, err := d.Source.Range(ctx, p.Source, p.From, p.To, paths)
	if err != nil {
		return sourceData{}, err
	}
	rng.WarnCommitCap(d.Logger, p.Source)
	prs, err := d.Source.PullRequests(ctx, p.Source, rng.Commits)
	if err != nil {
		return sourceData{}, err
	}
	prs, omitted := capPullRequests(prs, MaxPRContextBytes)
	if omitted > 0 {
		d.Logger.InfoContext(ctx, "PR context over its budget: oldest pull requests left out of the prompts",
			"source", p.Source, "omitted", omitted, "kept", len(prs))
	}
	return sourceData{patches: rng.Patches, prs: prs, omitted: omitted}, nil
}

// MaxPRContextBytes bounds the PR titles and bodies in one prompt, so the
// per-page budget does not grow with the number of PRs in the range.
const MaxPRContextBytes = 16 << 10

// capPullRequests keeps the newest PRs (highest numbers) whose titles and
// bodies fit in limit bytes, in number order, and returns how many it left out.
func capPullRequests(prs []plan.PullRequest, limit int) ([]plan.PullRequest, int) {
	size, keep := 0, len(prs)
	for keep > 0 {
		pr := prs[keep-1]
		if size+len(pr.Title)+len(pr.Body) > limit {
			break
		}
		size += len(pr.Title) + len(pr.Body)
		keep--
	}
	return prs[keep:], keep
}

// prepare renders every first-attempt prompt and checks the budget before any
// model call. Pages with an unavailable patch are dropped here.
func prepare(jobs []*job, kb knowledge, src sourceData, maxTokens int) error {
	var over []string
	for _, j := range jobs {
		hunks := make([]hunk, 0, len(j.page.SourcePaths))
		for _, sp := range j.page.SourcePaths {
			patch, ok := src.patches[sp]
			if !ok {
				j.result.Result, j.result.Reason = ResultDropped, ReasonPatchUnavailable
				break
			}
			hunks = append(hunks, hunk{Path: sp, Patch: patch})
		}
		if j.result.Result != "" {
			continue
		}
		typ := pageType(j.current)
		j.data = promptData{
			Profile:      kb.profile,
			Type:         typ,
			Template:     kb.template(typ),
			References:   kb.references(typ),
			Path:         j.page.Path,
			Page:         string(j.current),
			PullRequests: src.prs,
			PROmitted:    src.omitted,
			Hunks:        hunks,
		}
		var err error
		if j.prompt, err = render(j.data); err != nil {
			return err
		}
		if estimate(systemPrompt+j.prompt)+estimate(string(j.current)) > maxTokens {
			over = append(over, j.page.Path)
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("pages over --max-tokens-per-page %d: %s: no model call made", maxTokens, strings.Join(over, ", "))
	}
	return nil
}

// process runs up to two model calls for j and sets j.result and j.output. It
// returns the number of validation failures.
func process(ctx context.Context, j *job, f provenance.Fields, d Deps) (int, error) {
	budget := d.MaxTokensPerPage
	prompt := j.prompt
	failures := 0
	for {
		used := j.result.Tokens.total()
		est := estimate(systemPrompt + prompt)
		if used+est+estimate(string(j.current)) > budget {
			j.result.Result, j.result.Reason = ResultDropped, ReasonValidationFailed
			return failures, nil
		}
		reply, err := d.Model.Complete(ctx, model.Request{System: systemPrompt, User: prompt, MaxTokens: budget - used - est})
		if err != nil {
			return failures, fmt.Errorf("page %s: %w", j.page.Path, err)
		}
		j.result.Attempts++
		j.result.Tokens.add(usage(reply, est))

		out, unchanged, verr := accept(j, reply, f, d.Validator)
		if verr == nil {
			if unchanged {
				j.result.Result = ResultUnchanged
			} else {
				j.result.Result, j.output = ResultChanged, out
			}
			return failures, nil
		}
		failures++
		d.Logger.InfoContext(ctx, "page rejected", "page", j.page.Path, "attempt", j.result.Attempts, "error", verr.Error())
		if j.result.Attempts >= 2 {
			j.result.Result, j.result.Reason = ResultDropped, ReasonValidationFailed
			return failures, nil
		}
		retry := j.data
		retry.ValidationError = verr.Error()
		if prompt, err = render(retry); err != nil {
			return failures, err
		}
	}
}

// accept checks one reply. It returns the stamped page, whether it is the
// current page apart from provenance, or the error to show the model.
func accept(j *job, reply model.Reply, f provenance.Fields, v Validator) ([]byte, bool, error) {
	if reply.FinishReason == "length" {
		return nil, false, errors.New("the reply was cut off at the token limit")
	}
	text := unfence(strings.TrimSpace(reply.Text))
	if text == NoChange {
		return nil, true, nil
	}
	if !strings.HasPrefix(text, "---\n") {
		return nil, false, fmt.Errorf("the reply must be %s or a complete page starting with a --- frontmatter line", NoChange)
	}
	if m := promptMarker.FindString(text); m != "" {
		return nil, false, fmt.Errorf("the reply contains the prompt marker %s: reply with the current page only", m)
	}
	page := []byte(text + "\n")
	if err := v.Validate(j.page.Path, page); err != nil {
		return nil, false, err
	}
	if got, want := pageType(page), pageType(j.current); got != want {
		return nil, false, fmt.Errorf("the page type is %q but must stay %q", got, want)
	}
	if missing := missingKeys(j.current, page); len(missing) > 0 {
		return nil, false, fmt.Errorf("the reply drops the frontmatter keys %s: keep every existing key", strings.Join(missing, ", "))
	}
	out, err := provenance.Stamp(page, f)
	if err != nil {
		return nil, false, err
	}
	cur, err := provenance.Stamp(j.current, f)
	if err == nil && bytes.Equal(cur, out) {
		return nil, true, nil
	}
	return out, false, nil
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
// SIMPLIFIED: one token per three bytes, an upper bound for English and code
// with common tokenizers. Upgrade path: a tokenizer per model family.
func estimate(s string) int { return (len(s) + 2) / 3 }

// writePages writes every changed page. It runs only after all pages succeeded.
func writePages(root *os.Root, jobs []*job) error {
	var written []string
	for _, j := range jobs {
		if j.result.Result != ResultChanged {
			continue
		}
		if err := replaceFile(root, j.page.Path, j.output); err != nil {
			return fmt.Errorf("write page %s (already written: %s): %w", j.page.Path, strings.Join(written, ", "), err)
		}
		written = append(written, j.page.Path)
	}
	return nil
}

// replaceFile writes data to a temporary file next to name and renames it over
// name, so an interrupted write never leaves a half-written page.
func replaceFile(root *os.Root, name string, data []byte) (err error) {
	fi, err := root.Stat(name)
	if err != nil {
		return err
	}
	tmp := path.Join(path.Dir(name), "."+path.Base(name)+".okf-tmp")
	// A temp file left by an interrupted run is replaced, never followed.
	if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(tmp)
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
