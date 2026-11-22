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
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
)

// Version is the write report JSON contract version.
const Version = 2

// Page results.
const (
	ResultChanged   = "changed"
	ResultCreated   = "created"
	ResultUnchanged = "unchanged"
	ResultDropped   = "dropped"
)

// Drop reasons.
const (
	ReasonPatchUnavailable = "patch_unavailable"
	ReasonValidationFailed = "validation_failed"
	ReasonTemplateMissing  = "template_missing"
	ReasonPageMissing      = "page_missing"
	ReasonFileUnavailable  = "file_unavailable"
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
	// Files returns the content at ref of each path that is a text file of at
	// most plan.MaxSourceFileBytes. Other paths are absent from the map.
	Files(ctx context.Context, repo, ref string, paths []string) (map[string]string, error)
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

// DecodePlan reads one plan document of version 1 (from plan) or 2 (from
// reconcile). Unknown keys, other versions and a reconcile object that does
// not match the version or the pages fail.
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
	switch {
	case p.Version != plan.Version && p.Version != plan.ReconcileVersion:
		return nil, fmt.Errorf("plan version %d is not supported, want %d or %d", p.Version, plan.Version, plan.ReconcileVersion)
	case p.Version == plan.Version && p.Reconcile != nil:
		return nil, fmt.Errorf("decode plan: version %d has no reconcile object", plan.Version)
	case p.Version == plan.ReconcileVersion && p.Reconcile == nil:
		return nil, fmt.Errorf("plan version %d is not supported without a reconcile object", plan.ReconcileVersion)
	case p.Reconcile != nil:
		if err := checkReconcile(&p); err != nil {
			return nil, fmt.Errorf("decode plan: %w", err)
		}
	}
	return &p, nil
}

// checkReconcile checks that the reconcile pages match the plan pages one to
// one and that each names a known reason and evidence with a fitting base.
func checkReconcile(p *plan.Plan) error {
	pages := make(map[string]bool, len(p.Pages))
	for _, pg := range p.Pages {
		pages[pg.Path] = true
	}
	seen := make(map[string]bool, len(p.Reconcile.Pages))
	for _, rp := range p.Reconcile.Pages {
		switch {
		case !pages[rp.Path]:
			return fmt.Errorf("reconcile page %s is not a plan page", rp.Path)
		case seen[rp.Path]:
			return fmt.Errorf("reconcile page %s is listed twice", rp.Path)
		case !slices.Contains([]string{plan.ReasonBackfill, plan.ReasonStale, plan.ReasonRename, plan.ReasonWidened}, rp.Reason):
			return fmt.Errorf("reconcile page %s has unknown reason %q", rp.Path, rp.Reason)
		case !sortedPaths(rp.RoutedPaths):
			return fmt.Errorf("reconcile page %s has routed_paths that are not sorted, unique, non-empty paths", rp.Path)
		case rp.Evidence == plan.EvidenceHunks && !cursor.ValidSHA(rp.Base):
			return fmt.Errorf("reconcile page %s has evidence hunks but base %q is not a full commit SHA", rp.Path, rp.Base)
		case rp.Evidence == plan.EvidenceFiles && rp.Base != "":
			return fmt.Errorf("reconcile page %s has evidence files and a base", rp.Path)
		case rp.Evidence != plan.EvidenceHunks && rp.Evidence != plan.EvidenceFiles:
			return fmt.Errorf("reconcile page %s has unknown evidence %q", rp.Path, rp.Evidence)
		}
		seen[rp.Path] = true
	}
	for _, pg := range p.Pages {
		if !seen[pg.Path] {
			return fmt.Errorf("plan page %s has no reconcile entry", pg.Path)
		}
	}
	return nil
}

// sortedPaths reports whether ps is strictly increasing with no empty path.
func sortedPaths(ps []string) bool {
	for i, p := range ps {
		if p == "" || i > 0 && ps[i-1] >= p {
			return false
		}
	}
	return true
}

// Encode writes r as indented JSON. The same report always encodes to the same bytes.
func Encode(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// job is one update or create page on its way through the model.
type job struct {
	page    plan.Page
	create  bool
	current []byte // the page on disk; nil for a create
	src     *sourceData
	data    promptData
	system  string
	prompt  string
	result  PageResult
	output  []byte
}

// reserve is the text the page reply is expected to be about as long as: the
// current page, or the template for a create.
func (j *job) reserve() string {
	if j.create {
		return j.data.Template
	}
	return string(j.current)
}

// Run processes every page of p. Files under d.KnowledgeDir change only when
// Run returns a nil error, and then only the pages reported as changed or
// created.
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
			jobs = append(jobs, &job{page: pg, create: true, result: PageResult{Path: pg.Path}})
		case plan.StateMissing:
			rep.Pages = append(rep.Pages, PageResult{Path: pg.Path, Result: ResultDropped, Reason: ReasonPageMissing})
		default:
			return nil, fmt.Errorf("page %s: unknown state %q", pg.Path, pg.State)
		}
	}
	if len(jobs) > d.MaxPages {
		return nil, fmt.Errorf("plan has %d update and create pages, over --max-pages %d: no model call made", len(jobs), d.MaxPages)
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
		if j.create {
			if err := checkAbsent(root, j.page.Path); err != nil {
				return nil, err
			}
			continue
		}
		if j.current, err = root.ReadFile(j.page.Path); err != nil {
			return nil, fmt.Errorf("read page %s: %w", j.page.Path, err)
		}
	}

	if err := fetchSource(ctx, p, jobs, d); err != nil {
		return nil, err
	}
	if err := prepare(jobs, kb, d.MaxTokensPerPage); err != nil {
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
		f.RoutedPaths = routedPaths(p, j)
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

// routedPaths is the routed_paths to stamp on j's page: its reconcile entry's
// routed_paths, else the current page's routed_paths (none for a create) plus
// its source_paths, minus the paths the plan reports removed or renamed away.
// It is nil, an unknown set, when the current page records none.
func routedPaths(p *plan.Plan, j *job) []string {
	if rp, ok := p.ReconcilePage(j.page.Path); ok && rp.RoutedPaths != nil {
		return slices.Clone(rp.RoutedPaths)
	}
	var known []string
	if !j.create {
		f, _, err := provenance.Parse(j.current)
		if err != nil || f.RoutedPaths == nil {
			return nil
		}
		known = f.RoutedPaths
	}
	gone := map[string]bool{}
	for _, c := range p.ChangedPaths {
		switch {
		case c.Status == "removed":
			gone[c.Path] = true
		case c.Status == "renamed" && c.PreviousPath != "":
			gone[c.PreviousPath] = true
		}
	}
	out := []string{}
	for _, sp := range slices.Concat(known, j.page.SourcePaths) {
		if !gone[sp] {
			out = append(out, sp)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sourceData is what the source repo contributes to the prompts of a group of
// pages: the patches of one range with its PRs, or whole files (whole).
type sourceData struct {
	whole   bool
	patches map[string]string
	files   map[string]string
	prs     []plan.PullRequest
	omitted int // older PRs left out by capPullRequests
}

// fetchSource sets the source data of every job. A version 1 plan has one
// range with PR context; a reconcile plan has one range per page base, without
// PR context, and whole files at to for pages with evidence files.
func fetchSource(ctx context.Context, p *plan.Plan, jobs []*job, d Deps) error {
	if p.Reconcile == nil {
		src, err := fetchRange(ctx, p, p.From, jobs, d, true)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			j.src = src
		}
		return nil
	}
	byBase := map[string][]*job{}
	var whole []*job
	for _, j := range jobs {
		rp, _ := p.ReconcilePage(j.page.Path)
		if rp.Evidence == plan.EvidenceFiles {
			whole = append(whole, j)
		} else {
			byBase[rp.Base] = append(byBase[rp.Base], j)
		}
	}
	for _, base := range slices.Sorted(maps.Keys(byBase)) {
		src, err := fetchRange(ctx, p, base, byBase[base], d, false)
		if err != nil {
			return err
		}
		for _, j := range byBase[base] {
			j.src = src
		}
	}
	if len(whole) == 0 {
		return nil
	}
	files, err := d.Source.Files(ctx, p.Source, p.To, sourcePaths(whole))
	if err != nil {
		return err
	}
	src := &sourceData{whole: true, files: files}
	for _, j := range whole {
		j.src = src
	}
	return nil
}

func fetchRange(ctx context.Context, p *plan.Plan, base string, jobs []*job, d Deps, withPRs bool) (*sourceData, error) {
	rng, err := d.Source.Range(ctx, p.Source, base, p.To, sourcePaths(jobs))
	if err != nil {
		return nil, err
	}
	if !withPRs {
		return &sourceData{patches: rng.Patches}, nil
	}
	rng.WarnCommitCap(d.Logger, p.Source)
	prs, err := d.Source.PullRequests(ctx, p.Source, rng.Commits)
	if err != nil {
		return nil, err
	}
	prs, omitted := capPullRequests(prs, MaxPRContextBytes)
	if omitted > 0 {
		d.Logger.InfoContext(ctx, "PR context over its budget: oldest pull requests left out of the prompts",
			"source", p.Source, "omitted", omitted, "kept", len(prs))
	}
	return &sourceData{patches: rng.Patches, prs: prs, omitted: omitted}, nil
}

// sourcePaths is the sorted union of the jobs' source paths.
func sourcePaths(jobs []*job) []string {
	var paths []string
	for _, j := range jobs {
		paths = append(paths, j.page.SourcePaths...)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
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

// checkAbsent fails when a create page path exists under root, as any file
// type: the plan no longer matches the checkout.
func checkAbsent(root *os.Root, name string) error {
	_, err := root.Lstat(name)
	switch {
	case err == nil:
		return fmt.Errorf("page %s has state create but exists in the knowledge dir: re-run plan: no model call made", name)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("check create page %s: %w", name, err)
	}
}

// prepare renders every first-attempt prompt and checks the budget before any
// model call. Pages with an unavailable patch or file, and creates without a
// template, are dropped here.
func prepare(jobs []*job, kb knowledge, maxTokens int) error {
	var over []string
	for _, j := range jobs {
		if _, ok := kb.templates[j.page.Type]; j.create && !ok {
			j.result.Result, j.result.Reason = ResultDropped, ReasonTemplateMissing
			continue
		}
		src := j.src
		var hunks []hunk
		var files []sourceFile
		for _, sp := range j.page.SourcePaths {
			if src.whole {
				content, ok := src.files[sp]
				if !ok {
					j.result.Result, j.result.Reason = ResultDropped, ReasonFileUnavailable
					break
				}
				files = append(files, sourceFile{Path: sp, Content: content})
				continue
			}
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
		j.data = promptData{
			Profile:      kb.profile,
			Path:         j.page.Path,
			PullRequests: src.prs,
			PROmitted:    src.omitted,
			Hunks:        hunks,
			Files:        files,
		}
		if j.create {
			j.system = createSystemPrompt
			j.data.Create = true
			j.data.Type = j.page.Type
			j.data.Template = kb.template(j.page.Type)
			j.data.References = kb.sameTypeReferences(j.page.Type)
		} else {
			typ := pageType(j.current)
			j.system = systemPrompt
			j.data.Type = typ
			j.data.Template = kb.template(typ)
			j.data.References = kb.references(typ)
			j.data.Page = string(j.current)
		}
		var err error
		if j.prompt, err = render(j.data); err != nil {
			return err
		}
		if estimate(j.system+j.prompt)+estimate(j.reserve()) > maxTokens {
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
		est := estimate(j.system + prompt)
		if used+est+estimate(j.reserve()) > budget {
			j.result.Result, j.result.Reason = ResultDropped, ReasonValidationFailed
			return failures, nil
		}
		reply, err := d.Model.Complete(ctx, model.Request{System: j.system, User: prompt, MaxTokens: budget - used - est})
		if err != nil {
			return failures, fmt.Errorf("page %s: %w", j.page.Path, err)
		}
		j.result.Attempts++
		j.result.Tokens.add(usage(reply, est))

		out, unchanged, verr := accept(j, reply, f, d.Validator)
		if verr == nil {
			switch {
			case unchanged:
				j.result.Result = ResultUnchanged
			case j.create:
				j.result.Result, j.output = ResultCreated, out
			default:
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
	if text == NoChange && j.create {
		return nil, false, fmt.Errorf("the page does not exist yet: reply with the complete new page, not %s", NoChange)
	}
	if text == NoChange {
		return nil, true, nil
	}
	if !strings.HasPrefix(text, "---\n") && j.create {
		return nil, false, errors.New("the reply must be the complete new page starting with a --- frontmatter line")
	}
	if !strings.HasPrefix(text, "---\n") {
		return nil, false, fmt.Errorf("the reply must be %s or a complete page starting with a --- frontmatter line", NoChange)
	}
	if m := promptMarker.FindString(text); m != "" && j.create {
		return nil, false, fmt.Errorf("the reply contains the prompt marker %s: reply with the new page only", m)
	}
	if m := promptMarker.FindString(text); m != "" {
		return nil, false, fmt.Errorf("the reply contains the prompt marker %s: reply with the current page only", m)
	}
	page := []byte(text + "\n")
	if err := v.Validate(j.page.Path, page); err != nil {
		return nil, false, err
	}
	if j.create {
		if got := pageType(page); got != j.page.Type {
			return nil, false, fmt.Errorf("the page type is %q but must be %q", got, j.page.Type)
		}
		if missing := missingKeys([]byte(strings.ReplaceAll(j.data.Template, "\r\n", "\n")), page); len(missing) > 0 {
			return nil, false, fmt.Errorf("the reply lacks the template frontmatter keys %s", strings.Join(missing, ", "))
		}
		out, err := provenance.Stamp(page, f)
		return out, false, err
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

// writePages writes every changed and created page. It runs only after all
// pages succeeded.
func writePages(root *os.Root, jobs []*job) error {
	var written []string
	for _, j := range jobs {
		var err error
		switch j.result.Result {
		case ResultChanged:
			err = replaceFile(root, j.page.Path, j.output)
		case ResultCreated:
			err = createFile(root, j.page.Path, j.output)
		default:
			continue
		}
		if err != nil {
			return fmt.Errorf("write page %s (already written: %s): %w", j.page.Path, strings.Join(written, ", "), err)
		}
		written = append(written, j.page.Path)
	}
	return nil
}

// replaceFile writes data to a temporary file next to name and renames it over
// name, so an interrupted write never leaves a half-written page.
func replaceFile(root *os.Root, name string, data []byte) error {
	fi, err := root.Stat(name)
	if err != nil {
		return err
	}
	tmp, err := writeTemp(root, name, data, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// createFile writes data to a temporary file next to name and hard-links it
// to name. The link fails when name exists, so a create never overwrites a
// file, and an interrupted write never leaves a half-written page.
func createFile(root *os.Root, name string, data []byte) error {
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	tmp, err := writeTemp(root, name, data, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	return root.Link(tmp, name)
}

// writeTemp writes data to a new temporary file next to name and returns its
// path.
func writeTemp(root *os.Root, name string, data []byte, perm fs.FileMode) (tmp string, err error) {
	tmp = path.Join(path.Dir(name), "."+path.Base(name)+".okf-tmp")
	// A temp file left by an interrupted run is replaced, never followed.
	if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return "", err
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
	return tmp, err
}
