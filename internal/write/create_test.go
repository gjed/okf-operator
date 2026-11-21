package write

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/plan"
)

const (
	newPath = "product/services/new.md"
	newPage = "---\ntype: concept\ntitle: New\n---\n# New\n\nNew does a thing.\n"
)

func createPage(path, typ string, sources ...string) plan.Page {
	return plan.Page{Path: path, State: plan.StateCreate, Type: typ, SourcePaths: sources}
}

// expectedNewPage is newPage after a run with source src/a.go.
func expectedNewPage() string {
	return "---\ntype: concept\ntitle: New\n" +
		"source_repo: acme/api\n" +
		"source_sha: \"" + testHead + "\"\n" +
		"source_paths:\n  - src/a.go\n" +
		"updated_at: 2026-11-18T09:30:00Z\n" +
		"generated_by: okf-operator/1.2.3/m1\n" +
		"---\n# New\n\nNew does a thing.\n"
}

func TestRunCreatePage(t *testing.T) {
	e := newEnv(t)
	snap := tree(t, e.kb)
	e.llm.push(respond(newPage))
	rep, err := e.run(testPlan(createPage(newPath, "concept", "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Pages) != 1 {
		t.Fatalf("pages = %+v", rep.Pages)
	}
	if pg := rep.Pages[0]; pg.Path != newPath || pg.Result != ResultCreated || pg.Reason != "" || pg.Attempts != 1 {
		t.Errorf("page = %+v", pg)
	}
	if got := readFile(t, filepath.Join(e.kb, newPath)); got != expectedNewPage() {
		t.Errorf("file =\n%s\nwant\n%s", got, expectedNewPage())
	}
	fi, err := os.Lstat(filepath.Join(e.kb, newPath))
	if err != nil || !fi.Mode().IsRegular() {
		t.Errorf("created page is not a regular file: %v, %v", fi, err)
	}
	requireSameTree(t, snap, tree(t, e.kb), "product/services", filepath.FromSlash(newPath))

	reqs := e.llm.requests()
	if len(reqs) != 1 || reqs[0].System != createSystemPrompt {
		t.Fatalf("requests = %+v, want one call with the create system prompt", reqs)
	}
	user := reqs[0].User
	for _, want := range []string{
		"<<<WRITING-GUIDANCE>>>\nWrite short sentences.",
		"<<<PAGE-TEMPLATE type=\"concept\">>>\n# Title\n\nSummary.\n<<<END-PAGE-TEMPLATE>>>",
		"<<<REFERENCE-PAGE path=\"product/a.md\">>>",
		"<<<REFERENCE-PAGE path=\"product/b.md\">>>",
		"Diff of src/a.go:\n\n" + srcPatchA,
		"Pull request #7: Rename the thing",
		"with `type: concept`",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt lacks %q:\n%s", want, user)
		}
	}
	for _, unwanted := range []string{"product/c.md", "CURRENT-PAGE", "src/b.go"} {
		if strings.Contains(user, unwanted) {
			t.Errorf("prompt contains %q", unwanted)
		}
	}
}

// A route type with no template is dropped before any model call.
func TestRunCreateWithoutTemplateDropped(t *testing.T) {
	e := newEnv(t)
	snap := tree(t, e.kb)
	rep, err := e.run(testPlan(createPage(newPath, "runbook", "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if pg := rep.Pages[0]; pg.Result != ResultDropped || pg.Reason != ReasonTemplateMissing || pg.Attempts != 0 {
		t.Errorf("page = %+v", pg)
	}
	e.requireNoModelCall()
	requireSameTree(t, snap, tree(t, e.kb))
}

// Invalid create replies are retried once and then dropped without a file.
func TestRunCreateInvalidReply(t *testing.T) {
	cases := map[string]struct {
		template, reply, wantErr string
	}{
		"type differs from the route": {
			reply:   strings.Replace(newPage, "type: concept", "type: guide", 1),
			wantErr: `the page type is "guide" but must be "concept"`,
		},
		"no change": {
			reply:   "NO_CHANGE",
			wantErr: "the page does not exist yet",
		},
		"template key dropped": {
			template: "---\ntype: concept\nowner: \"\"\n---\n# Title\n",
			reply:    newPage,
			wantErr:  "the reply lacks the template frontmatter keys owner",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if tc.template != "" {
				writeFile(t, e.kb, ".kb/templates/concept.md", tc.template)
			}
			snap := tree(t, e.kb)
			e.llm.push(respond(tc.reply), respond(tc.reply))
			rep, err := e.run(testPlan(createPage(newPath, "concept", "src/a.go")))
			if err != nil {
				t.Fatal(err)
			}
			if pg := rep.Pages[0]; pg.Result != ResultDropped || pg.Reason != ReasonValidationFailed || pg.Attempts != 2 {
				t.Errorf("page = %+v", pg)
			}
			reqs := e.llm.requests()
			if len(reqs) != 2 || !strings.Contains(reqs[1].User, tc.wantErr) {
				t.Errorf("retry prompt lacks %q: %+v", tc.wantErr, reqs)
			}
			requireSameTree(t, snap, tree(t, e.kb))
		})
	}
}

// A create whose path exists in the checkout, as any file type, fails the run
// before any GitHub or model call.
func TestRunCreateExistingPathFails(t *testing.T) {
	cases := map[string]func(t *testing.T, kb string){
		"regular file": func(*testing.T, string) {},
		"symlink": func(t *testing.T, kb string) {
			if err := os.Symlink("nowhere.md", filepath.Join(kb, filepath.FromSlash(newPath))); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			path := betaPath
			if name == "symlink" {
				path = newPath
				if err := os.MkdirAll(filepath.Join(e.kb, "product/services"), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			setup(t, e.kb)
			snap := tree(t, e.kb)
			before := e.githubRequests()
			_, err := e.run(testPlan(createPage(path, "concept", "src/a.go")))
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("err = %v, want an error naming %s", err, path)
			}
			e.requireNoModelCall()
			e.requireNoGitHubCall(before)
			requireSameTree(t, snap, tree(t, e.kb))
		})
	}
}

// Creates count against --max-pages with updates.
func TestRunCreatesCountAgainstMaxPages(t *testing.T) {
	e := newEnv(t)
	_, err := e.run(testPlan(updatePage(alphaPath, "src/a.go"), createPage(newPath, "concept", "src/a.go")), maxPages(1))
	if err == nil || !strings.Contains(err.Error(), "--max-pages 1") {
		t.Fatalf("err = %v, want the --max-pages error", err)
	}
	e.requireNoModelCall()
}

// A model failure after a create was answered leaves the checkout untouched.
func TestRunCreateThenModelFailureCreatesNothing(t *testing.T) {
	e := newEnv(t)
	snap := tree(t, e.kb)
	e.llm.push(respond(newPage), failWith(500))
	_, err := e.run(testPlan(createPage(newPath, "concept", "src/a.go"), updatePage(alphaPath, "src/a.go")))
	if err == nil {
		t.Fatal("Run succeeded after HTTP 500")
	}
	requireSameTree(t, snap, tree(t, e.kb))
}

// createFile never replaces an existing file.
func TestCreateFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "product/x.md", "keep\n")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := createFile(root, "product/x.md", []byte("new\n")); err == nil {
		t.Fatal("createFile replaced an existing file")
	}
	if got := readFile(t, filepath.Join(dir, "product/x.md")); got != "keep\n" {
		t.Errorf("file = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "product/.x.md.okf-tmp")); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}
