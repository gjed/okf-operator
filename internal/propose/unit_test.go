package propose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

func TestDecodeReport(t *testing.T) {
	tests := []struct{ name, doc, want string }{
		{"ok", `{"version":3,"source":"acme/api","pages":[]}`, ""},
		{"version 1", `{"version":1}`, "report version 1 is not supported"},
		{"version 2 from an older write", `{"version":2,"source":"acme/api","pages":[]}`, "report version 2 is not supported, want 3: re-run write"},
		{"version 4", `{"version":4}`, "report version 4 is not supported"},
		{"unknown key", `{"version":3,"extra":true}`, "unknown field"},
		{"two documents", `{"version":3} {"version":3}`, "more than one JSON document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeReport(strings.NewReader(tt.doc))
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAgree(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f *fixture)
		want   string
	}{
		{"ok", func(*fixture) {}, ""},
		{"source", func(f *fixture) { f.rep.Source = "acme/other" }, "disagree on source"},
		{"bundle", func(f *fixture) { f.rep.Bundle = "other" }, "disagree on bundle"},
		{"from", func(f *fixture) { f.rep.From = commit1 }, "disagree on from"},
		{"to", func(f *fixture) { f.rep.To = commit1 }, "disagree on to"},
		{"page outside the plan", func(f *fixture) { f.rep.Pages[0].Path = "docs/z.md" }, "docs/z.md is not a plan page"},
		{"plan page missing", func(f *fixture) { f.rep.Pages = f.rep.Pages[1:] }, "docs/a.md is missing from the report"},
		{"page twice", func(f *fixture) { f.rep.Pages[1].Path = "docs/a.md" }, "twice"},
		{"unknown result", func(f *fixture) { f.rep.Pages[1].Result = "maybe" }, "unknown result"},
		{"restamped on a version 1 plan", func(f *fixture) { f.rep.Pages[1].Result = write.ResultRestamped }, "docs/b.md is restamped but the plan is not a reconcile plan"},
		{"restamped on a reconcile plan", func(f *fixture) {
			f.asReconcile(plan.CursorHead)
			f.rep.Pages[1].Result = write.ResultRestamped
		}, ""},
		{"created page outside the plan", func(f *fixture) {
			f.rep.Pages[0] = write.PageResult{Path: "docs/new.md", Result: write.ResultCreated}
		}, "docs/new.md is not a plan page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fixture{}
			full := newFixture(t)
			f.plan, f.rep = full.plan, full.rep
			tt.mutate(f)
			err := Agree(f.plan, f.rep)
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func baseTree() map[string]Entry {
	tree := map[string]Entry{}
	for p, c := range baseFiles {
		tree[p] = Entry{Mode: modeFile, SHA: blobSHA([]byte(c))}
	}
	return tree
}

func TestVerify(t *testing.T) {
	changed := map[string]bool{"docs/a.md": true}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string, tree map[string]Entry)
		want  string
	}{
		{"clean", func(*testing.T, string, map[string]Entry) {}, ""},
		{"unreported modified", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "docs/b.md", "x")
		}, "docs/b.md: differs"},
		{"unreported new", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "notes.txt", "x")
		}, "notes.txt: not in the base commit"},
		{"deleted", func(t *testing.T, dir string, _ map[string]Entry) {
			if err := os.Remove(filepath.Join(dir, "docs/c.md")); err != nil {
				t.Fatal(err)
			}
		}, "docs/c.md: missing from the checkout"},
		{"changed identical to base", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "docs/a.md", pageA)
		}, "docs/a.md: reported changed but identical"},
		{"symlink replaces a file", func(t *testing.T, dir string, _ map[string]Entry) {
			if err := os.Remove(filepath.Join(dir, "docs/b.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("c.md", filepath.Join(dir, "docs/b.md")); err != nil {
				t.Fatal(err)
			}
		}, "docs/b.md: differs"},
		{"symlink kept", func(t *testing.T, dir string, tree map[string]Entry) {
			if err := os.Symlink("docs/c.md", filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
			tree["link"] = Entry{Mode: modeSymlink, SHA: blobSHA([]byte("docs/c.md"))}
		}, ""},
		{"submodule skipped", func(t *testing.T, dir string, tree map[string]Entry) {
			writeFile(t, dir, "vendor/sub/file", "x")
			tree["vendor/sub"] = Entry{Mode: modeSubmodule, SHA: commit1}
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, c := range baseFiles {
				writeFile(t, dir, p, c)
			}
			writeFile(t, dir, "docs/a.md", pageANew)
			writeFile(t, dir, ".git/config", "x")
			tree := baseTree()
			tt.setup(t, dir, tree)
			files, err := Verify(dir, tree, changed, nil)
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if err == nil && (len(files) != 1 || string(files["docs/a.md"].Content) != pageANew || files["docs/a.md"].Mode != modeFile) {
				t.Fatalf("files = %v", files)
			}
		})
	}
}

func TestVerifyCreated(t *testing.T) {
	created := map[string]bool{"docs/new.md": true}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string, tree map[string]Entry)
		want  string
	}{
		{"new file", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "docs/new.md", pageANew)
		}, ""},
		{"new file in a new directory is still only the created page", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "docs/new.md", pageANew)
			writeFile(t, dir, "docs/sub/other.md", "x")
		}, "docs/sub/other.md: not in the base commit and not a changed page"},
		{"unreported new file next to it", func(t *testing.T, dir string, _ map[string]Entry) {
			writeFile(t, dir, "docs/new.md", pageANew)
			writeFile(t, dir, "docs/extra.md", "x")
		}, "docs/extra.md: not in the base commit"},
		{"already in the base", func(t *testing.T, dir string, tree map[string]Entry) {
			writeFile(t, dir, "docs/new.md", pageANew)
			tree["docs/new.md"] = Entry{Mode: modeFile, SHA: blobSHA([]byte(pageA))}
		}, "docs/new.md: reported created but already in the base commit"},
		{"symlink", func(t *testing.T, dir string, _ map[string]Entry) {
			if err := os.Symlink("a.md", filepath.Join(dir, "docs/new.md")); err != nil {
				t.Fatal(err)
			}
		}, "docs/new.md: created page is a symlink"},
		{"missing from the checkout", func(*testing.T, string, map[string]Entry) {}, "docs/new.md: created page is missing from the checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, c := range baseFiles {
				writeFile(t, dir, p, c)
			}
			tree := baseTree()
			tt.setup(t, dir, tree)
			files, err := Verify(dir, tree, nil, created)
			if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if err == nil && (len(files) != 1 || string(files["docs/new.md"].Content) != pageANew || files["docs/new.md"].Mode != modeFile) {
				t.Fatalf("files = %v", files)
			}
		})
	}
}

func TestVerifyListsAtMostTwenty(t *testing.T) {
	dir := t.TempDir()
	for i := range 30 {
		writeFile(t, dir, filepath.Join("x", string(rune('a'+i%26))+strings.Repeat("z", i/26)+".md"), "x")
	}
	_, err := Verify(dir, map[string]Entry{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "and 10 more") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppendSectionCapsBody(t *testing.T) {
	sec := func(i int) string {
		return markerPrefix + strings.Repeat("0", 40) + ".." + strings.Repeat("1", 40) + " -->\n### run " + string(rune('A'+i)) + "\n" + strings.Repeat("x", 20000) + "\n"
	}
	body := "intro\n\n" + sec(0)
	for i := 1; i < 6; i++ {
		body = appendSection(body, sec(i))
		if n := len([]rune(body)); n > MaxBody {
			t.Fatalf("body has %d characters after section %d", n, i)
		}
	}
	if !strings.HasPrefix(body, "intro\n\n"+omittedTag+" 3 earlier ranges omitted") {
		t.Fatalf("body head = %q", body[:120])
	}
	if !strings.Contains(body, "### run F") || strings.Contains(body, "### run C") {
		t.Fatal("want the newest sections kept and the oldest dropped")
	}
}

func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	})

	f := newFixture(t)
	ctx, span := tp.Tracer("test").Start(t.Context(), "propose")
	res, err := Run(ctx, f.plan, f.rep, f.deps(t))
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range spans.Ended()[0].Attributes() {
		attrs[kv.Key] = kv.Value
	}
	if attrs["okf.propose.action"].AsString() != ActionOpened || attrs["okf.propose.pr"].AsInt64() != int64(res.PR.Number) ||
		!attrs["okf.propose.cursor_advanced"].AsBool() || attrs["okf.propose.pages.committed"].AsInt64() != 1 ||
		attrs["okf.source.repo"].AsString() != testSource || attrs["okf.bundle"].AsString() != testBundle {
		t.Fatalf("span attributes = %v", attrs)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				if v, _ := dp.Attributes.Value("okf.source.repo"); v.AsString() != testSource {
					t.Fatalf("%s lacks the source label", m.Name)
				}
				got[m.Name] += dp.Value
			}
		}
	}
	want := map[string]int64{"okf.propose.prs.opened": 1, "okf.propose.prs.updated": 0, "okf.propose.cursor.advances": 1}
	for k, v := range want {
		if n, ok := got[k]; !ok || n != v {
			t.Errorf("%s = %d (present %v), want %d", k, n, ok, v)
		}
	}
}
