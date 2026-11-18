package write

import (
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
)

const (
	testSource    = "acme/api"
	testBundle    = "product"
	testCursor    = "1111111111111111111111111111111111111111"
	testHead      = "2222222222222222222222222222222222222222"
	testCommit    = "3333333333333333333333333333333333333333"
	testModelName = "m1"
	testVersion   = "1.2.3"
	testToken     = "ghp_test0123456789"
	untrustedEnd  = "<<<END-UNTRUSTED-SOURCE-DATA>>>"
)

var testNow = time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)

const (
	alphaPage = "---\ntype: concept\ntitle: Alpha\n---\n# Alpha\n\nAlpha does the thing.\n"
	alphaNew  = "---\ntype: concept\ntitle: Alpha\n---\n# Alpha\n\nAlpha does the new thing.\n"
	betaPage  = "---\ntype: concept\ntitle: Beta\n---\n# Beta\n\nBeta body.\n"
	betaNew   = "---\ntype: concept\ntitle: Beta\n---\n# Beta\n\nBeta changed.\n"
	srcPatchA = "@@ -1 +1 @@\n-Old line.\n+New line.\n"
	srcPatchB = "@@ -2 +2 @@\n-Old two.\n+New two.\n"
)

// writeFile creates rel under dir, making parent directories.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newKnowledge writes the checkout every run test starts from: the pages
// product/a.md (planned by most tests) and product/b.md (same type, a
// reference), a guide reference, reserved files, a profile and a template.
func newKnowledge(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"product/a.md":             alphaPage,
		"product/b.md":             betaPage,
		"product/c.md":             "---\ntype: guide\ntitle: Gamma\n---\n# Gamma\n\nGuide body.\n",
		"product/index.md":         "---\ntype: concept\ntitle: Index\n---\n# Index\n",
		"product/log.md":           "---\ntype: concept\n---\nlog\n",
		".kb/profile.md":           "Write short sentences.\n",
		".kb/templates/concept.md": "# Title\n\nSummary.\n",
	} {
		writeFile(t, dir, rel, content)
	}
	return dir
}

// tree snapshots every entry under dir: mode and content for files, the
// target for symlinks, and a marker for directories.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			out[rel] = "dir"
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "symlink " + target
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = fi.Mode().String() + "\n" + string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// requireSameTree fails for every entry of before that changed, vanished or
// appeared in after, ignoring the paths in except.
func requireSameTree(t *testing.T, before, after map[string]string, except ...string) {
	t.Helper()
	b, a := maps.Clone(before), maps.Clone(after)
	for _, p := range except {
		delete(b, p)
		delete(a, p)
	}
	for _, k := range slices.Sorted(maps.Keys(b)) {
		if v, ok := a[k]; !ok {
			t.Errorf("%s was removed", k)
		} else if v != b[k] {
			t.Errorf("%s was modified", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(a)) {
		if _, ok := b[k]; !ok {
			t.Errorf("%s was created", k)
		}
	}
}

// scripted is one canned model reply.
type scripted struct {
	status int // non-zero: answer with this HTTP status instead
	text   string
	finish string
	usage  *[2]int // nil: the reply carries no usage block
}

func respond(text string) scripted { return scripted{text: text} }

func withUsage(s scripted, prompt, completion int) scripted {
	s.usage = &[2]int{prompt, completion}
	return s
}

func withFinish(s scripted, finish string) scripted {
	s.finish = finish
	return s
}

func failWith(status int) scripted { return scripted{status: status} }

// recorded is one request the model endpoint received.
type recorded struct {
	System, User string
	MaxTokens    int
}

// fakeLLM is an OpenAI-compatible chat completions endpoint that answers
// scripted replies in order and records every request. It fails with 500
// once the script runs out, so an unscripted call is visible.
type fakeLLM struct {
	*httptest.Server
	mu     sync.Mutex
	script []scripted
	reqs   []recorded
}

func newFakeLLM(t *testing.T) *fakeLLM {
	t.Helper()
	f := &fakeLLM{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeLLM) push(s ...scripted) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = append(f.script, s...)
}

func (f *fakeLLM) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

func (f *fakeLLM) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{System: req.Messages[0].Content, User: req.Messages[1].Content, MaxTokens: req.MaxTokens})
	if len(f.script) == 0 {
		f.mu.Unlock()
		http.Error(w, "script exhausted", http.StatusInternalServerError)
		return
	}
	s := f.script[0]
	f.script = f.script[1:]
	f.mu.Unlock()

	if s.status != 0 {
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, `{"error":"scripted failure"}`)
		return
	}
	finish := s.finish
	if finish == "" {
		finish = "stop"
	}
	resp := map[string]any{"choices": []any{map[string]any{
		"message":       map[string]string{"role": "assistant", "content": s.text},
		"finish_reason": finish,
	}}}
	if s.usage != nil {
		resp["usage"] = map[string]int{"prompt_tokens": s.usage[0], "completion_tokens": s.usage[1]}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// env is the real stack a write run talks to: a GitHub fake behind a token
// Provider, a model endpoint fake behind a real model.Client, and a checkout.
type env struct {
	t        *testing.T
	kb       string
	gh       *githubtest.Fake
	llm      *fakeLLM
	client   *model.Client
	provider *github.Provider
}

// newEnv starts a fresh stack. The default range changes src/a.go and
// src/b.go, and the commit's PR #7 is merged.
func newEnv(t *testing.T) *env {
	t.Helper()
	gh := githubtest.New(t)
	gh.SetRepo(testSource, "main", testHead)
	gh.SetCompare(testSource, testCursor, testHead, compareJSON(t,
		patched("src/a.go", srcPatchA), patched("src/b.go", srcPatchB)))
	gh.SetCommitPulls(testSource, testCommit, pullsJSON(t, merged(7, "Rename the thing", "Renames the thing.\n")))
	p, err := github.NewProvider(github.Config{APIURL: gh.APIURL(), Token: testToken},
		github.ReadContents(testSource), &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	llm := newFakeLLM(t)
	return &env{
		t:        t,
		kb:       newKnowledge(t),
		gh:       gh,
		llm:      llm,
		client:   &model.Client{BaseURL: llm.URL + "/v1", Model: testModelName},
		provider: p,
	}
}

// setRange replaces the compare response of the default range.
func (e *env) setRange(files ...map[string]any) {
	e.gh.SetCompare(testSource, testCursor, testHead, compareJSON(e.t, files...))
}

// setPulls replaces the PRs listed for the default range's commit.
func (e *env) setPulls(prs ...map[string]any) {
	e.gh.SetCommitPulls(testSource, testCommit, pullsJSON(e.t, prs...))
}

// githubRequests counts requests the GitHub fake has received.
func (e *env) githubRequests() int { return len(e.gh.Auths()) }

// deps builds the production wiring with the test doubles, then applies opts.
func (e *env) deps(opts ...func(*Deps)) Deps {
	d := Deps{
		Source:           plan.GitHubDiffer{Clients: e.provider},
		Model:            e.client,
		Validator:        BaseValidator{},
		KnowledgeDir:     e.kb,
		ModelName:        testModelName,
		ToolVersion:      testVersion,
		MaxPages:         10,
		MaxTokensPerPage: 16000,
		Now:              func() time.Time { return testNow },
		Logger:           slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(&d)
	}
	return d
}

func (e *env) run(p *plan.Plan, opts ...func(*Deps)) (*Report, error) {
	return Run(e.t.Context(), p, e.deps(opts...))
}

func maxPages(n int) func(*Deps)  { return func(d *Deps) { d.MaxPages = n } }
func maxTokens(n int) func(*Deps) { return func(d *Deps) { d.MaxTokensPerPage = n } }

// testPlan is a plan for the default range over the given pages.
func testPlan(pages ...plan.Page) *plan.Plan {
	return &plan.Plan{
		Version:      plan.Version,
		Source:       testSource,
		Bundle:       testBundle,
		From:         testCursor,
		To:           testHead,
		ChangedPaths: []plan.ChangedPath{},
		Pages:        pages,
		Unrouted:     []string{},
		Excluded:     []string{},
	}
}

func updatePage(path string, sources ...string) plan.Page {
	return plan.Page{Path: path, State: plan.StateUpdate, Type: "concept", SourcePaths: sources}
}

func patched(name, patch string) map[string]any {
	return map[string]any{"filename": name, "status": "modified", "changes": 2, "patch": patch}
}

// unpatched is a changed file whose patch the compare API omitted.
func unpatched(name string) map[string]any {
	return map[string]any{"filename": name, "status": "modified", "changes": 5}
}

func merged(number int, title, body string) map[string]any {
	return map[string]any{"number": number, "title": title, "body": body, "state": "closed", "merged_at": "2026-11-10T10:00:00Z"}
}

func compareJSON(t *testing.T, files ...map[string]any) []byte {
	t.Helper()
	body := map[string]any{
		"status":        "ahead",
		"total_commits": 1,
		"commits":       []map[string]string{{"sha": testCommit}},
		"files":         files,
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pullsJSON(t *testing.T, prs ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// probePrompt returns the first user message a run of p sends when the
// model answers NO_CHANGE, using a fresh stack with the default fixtures.
func probePrompt(t *testing.T, p *plan.Plan) string {
	t.Helper()
	e := newEnv(t)
	e.llm.push(respond("NO_CHANGE"))
	if _, err := e.run(p); err != nil {
		t.Fatal(err)
	}
	return e.llm.requests()[0].User
}

// requireNoModelCall fails when the model endpoint received any request.
func (e *env) requireNoModelCall() {
	e.t.Helper()
	if n := len(e.llm.requests()); n != 0 {
		e.t.Errorf("model received %d requests, want 0", n)
	}
}

// requireNoGitHubCall fails when GitHub received a request after githubBefore.
func (e *env) requireNoGitHubCall(githubBefore int) {
	e.t.Helper()
	if n := e.githubRequests() - githubBefore; n != 0 {
		e.t.Errorf("GitHub received %d requests, want 0", n)
	}
}
