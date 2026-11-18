package write

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/plan"
)

// planJSON is a valid plan v1 document with one update page, with mutate
// applied to its top-level object first.
func planJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	doc := map[string]any{
		"version": 1, "source": testSource, "bundle": testBundle,
		"from": testCursor, "to": testHead, "initial": false,
		"changed_paths": []any{},
		"pages": []any{map[string]any{
			"path": "product/a.md", "state": "update", "type": "concept", "source_paths": []string{"src/a.go"},
		}},
		"unrouted": []string{}, "excluded": []string{},
	}
	if mutate != nil {
		mutate(doc)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDecodePlan(t *testing.T) {
	cases := map[string]struct {
		in      string
		wantErr bool
	}{
		"valid": {in: planJSON(t, nil)},
		"version 2": {in: planJSON(t, func(d map[string]any) {
			d["version"] = 2
		}), wantErr: true},
		"version 0": {in: planJSON(t, func(d map[string]any) {
			d["version"] = 0
		}), wantErr: true},
		"unknown top-level key": {in: planJSON(t, func(d map[string]any) {
			d["extra"] = true
		}), wantErr: true},
		"unknown page key": {in: planJSON(t, func(d map[string]any) {
			d["pages"].([]any)[0].(map[string]any)["extra"] = 1
		}), wantErr: true},
		"two documents": {in: planJSON(t, nil) + planJSON(t, nil), wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := DecodePlan(strings.NewReader(tc.in))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err == nil && (p.Source != testSource || len(p.Pages) != 1) {
				t.Fatalf("decoded %+v", p)
			}
		})
	}
}

// DecodePlan reads what plan.Encode writes.
func TestDecodePlanRoundTrip(t *testing.T) {
	want := testPlan(updatePage("product/a.md", "src/a.go"))
	var buf bytes.Buffer
	if err := plan.Encode(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := DecodePlan(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestCheckPagePath(t *testing.T) {
	valid := []string{"product/a.md", "product/sub/a.md", "product/.hidden/a.md"}
	invalid := map[string]string{
		"parent escape":       "../x.md",
		"parent in the path":  "product/../x.md",
		"absolute":            "/product/a.md",
		"unclean":             "product//a.md",
		"trailing slash":      "product/a.md/",
		"backslash":           `product\a.md`,
		"empty":               "",
		"not markdown":        "product/a.txt",
		"upper-case markdown": "product/a.MD",
		"outside bundle":      "other/a.md",
		"bundle prefix only":  "productx/a.md",
		"inside .kb":          "product/.kb/a.md",
		"bundle dot-kb":       ".kb/a.md",
	}
	for _, p := range valid {
		if err := checkPagePath(testBundle, p); err != nil {
			t.Errorf("checkPagePath(%q) = %v, want nil", p, err)
		}
	}
	for name, p := range invalid {
		if err := checkPagePath(testBundle, p); err == nil {
			t.Errorf("%s: checkPagePath(%q) = nil, want error", name, p)
		}
	}
	if err := checkPagePath("", "product/a.md"); err == nil {
		t.Error("an empty bundle accepts no page")
	}
}

// Every rejected plan path fails Run before the source or the model is touched.
func TestRunRejectsUnsafePagePathsBeforeAnyCall(t *testing.T) {
	for _, bad := range []string{"../x.md", "product/../../x.md", "other/page.md", "product/.kb/a.md", "product/a.txt", "/product/a.md"} {
		t.Run(bad, func(t *testing.T) {
			e := newEnv(t)
			before := e.githubRequests()
			_, err := e.run(testPlan(updatePage(bad, "src/a.go")))
			if err == nil {
				t.Fatal("Run accepted the page path")
			}
			e.requireNoModelCall()
			e.requireNoGitHubCall(before)
		})
	}
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// References: same type first, then the rest, each group sorted by path, at
// most two, skipping reserved files, planned pages, .kb/ and pages without a
// type.
func TestReferencesOrderAndExclusions(t *testing.T) {
	dir := newKnowledge(t)
	writeFile(t, dir, "product/sub/d.md", "---\ntype: concept\n---\nD\n")
	writeFile(t, dir, "product/sub/index.md", "---\ntype: concept\n---\nReserved in a subdirectory.\n")
	writeFile(t, dir, "product/.kb/x.md", "---\ntype: concept\n---\nX\n")
	writeFile(t, dir, "product/notype.md", "---\ntitle: N\n---\nN\n")
	writeFile(t, dir, "product/plain.md", "no frontmatter\n")
	root := openRoot(t, dir)
	planned := map[string]bool{"product/a.md": true}

	cases := map[string][]string{
		"concept": {"product/b.md", "product/sub/d.md"},
		"guide":   {"product/c.md", "product/b.md"},
		"":        {"product/b.md", "product/c.md"},
	}
	for typ, want := range cases {
		t.Run("type "+typ, func(t *testing.T) {
			k, err := loadKnowledge(root, testBundle, planned)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range k.references(typ) {
				got = append(got, r.Path)
			}
			if !slices.Equal(got, want) {
				t.Errorf("references = %v, want %v", got, want)
			}
			again, err := loadKnowledge(root, testBundle, planned)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(k.references(typ), again.references(typ)) {
				t.Error("references differ between two loads of the same checkout")
			}
		})
	}
}

func TestLoadKnowledgeProfileAndTemplates(t *testing.T) {
	kb := newKnowledge(t)
	root := openRoot(t, kb)
	k, err := loadKnowledge(root, testBundle, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.profile != "Write short sentences." {
		t.Errorf("profile = %q", k.profile)
	}
	if got := k.template("concept"); got != "# Title\n\nSummary." {
		t.Errorf("template = %q", got)
	}
	if got := k.template("../concept"); got != "" {
		t.Errorf("a type that is not a segment selected template %q", got)
	}
}

// A missing profile, a missing templates directory and a missing bundle are
// all allowed: the prompt just has fewer sections.
func TestLoadKnowledgeMissingOptionalFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "product/a.md", alphaPage)
	root := openRoot(t, dir)
	k, err := loadKnowledge(root, testBundle, map[string]bool{"product/a.md": true})
	if err != nil {
		t.Fatalf("missing profile and templates: %v", err)
	}
	if k.profile != "" || len(k.templates) != 0 || len(k.pages) != 0 {
		t.Errorf("knowledge = %+v, want empty", k)
	}
	if _, err := loadKnowledge(root, "absent", nil); err != nil {
		t.Errorf("missing bundle directory: %v", err)
	}
}
