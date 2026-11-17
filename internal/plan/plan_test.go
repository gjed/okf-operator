package plan

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/routing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	cursorSHA = "1111111111111111111111111111111111111111"
	headSHA   = "2222222222222222222222222222222222222222"
	pinnedSHA = "3333333333333333333333333333333333333333"
)

type fakeCursor struct {
	sha   string
	found bool
	err   error
}

func (f fakeCursor) Read(context.Context, string) (string, bool, error) { return f.sha, f.found, f.err }

type fakeDiffer struct {
	changes   []ChangedPath
	err       error
	headCalls int
	ranges    []string
}

func (f *fakeDiffer) Head(context.Context, string) (string, error) {
	f.headCalls++
	return headSHA, nil
}

func (f *fakeDiffer) Changes(_ context.Context, _, base, head string) ([]ChangedPath, error) {
	f.ranges = append(f.ranges, base+"..."+head)
	return f.changes, f.err
}

type fakePages map[string]bool

func (f fakePages) Exists(page string) (bool, error) { return f[page], nil }

func routingMap(t *testing.T) *routing.Map {
	t.Helper()
	m, err := routing.Parse([]byte(`version: 1
sources:
  - repo: acme/platform
    bundle: infra
    exclude: ["**/secrets/**"]
    routes:
      - page: infra/pipelines/deploy.md
        paths: ["deploy/**"]
      - page: infra/services/api.md
        paths: ["src/api/**", "deploy/api.yaml"]
      - page: infra/services/billing.md
        type: Service
        paths: ["billing/**"]
      - page: infra/services/legacy.md
        paths: ["old/**"]
`))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type run struct {
	plan   *Plan
	err    error
	differ *fakeDiffer
	logs   string
}

func build(t *testing.T, source, to string, c fakeCursor, d *fakeDiffer) run {
	t.Helper()
	return buildWith(context.Background(), t, source, to, c, d)
}

// buildCtx plans acme/platform from cursorSHA to the fake head under ctx.
func buildCtx(ctx context.Context, t *testing.T, d *fakeDiffer) run {
	t.Helper()
	return buildWith(ctx, t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, d)
}

func buildWith(ctx context.Context, t *testing.T, source, to string, c fakeCursor, d *fakeDiffer) run {
	t.Helper()
	var buf bytes.Buffer
	p, err := Build(ctx, source, to, Deps{
		Routing: routingMap(t),
		Cursor:  c,
		Differ:  d,
		Pages:   fakePages{"infra/pipelines/deploy.md": true, "infra/services/api.md": true},
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
	})
	return run{p, err, d, buf.String()}
}

func empty(source, bundle, from, to string, initial bool) *Plan {
	return &Plan{Version: 1, Source: source, Bundle: bundle, From: from, To: to, Initial: initial,
		ChangedPaths: []ChangedPath{}, Pages: []Page{}, Unrouted: []string{}, Excluded: []string{}}
}

func TestBuildNotOnboarded(t *testing.T) {
	r := build(t, "acme/unknown", "", fakeCursor{}, &fakeDiffer{})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !reflect.DeepEqual(r.plan, empty("acme/unknown", "", "", "", false)) {
		t.Fatalf("plan = %+v", r.plan)
	}
	if !strings.Contains(r.logs, "source not onboarded") || r.differ.headCalls != 0 {
		t.Fatalf("logs %q, head calls %d", r.logs, r.differ.headCalls)
	}
}

func TestBuildInitial(t *testing.T) {
	r := build(t, "acme/platform", "", fakeCursor{}, &fakeDiffer{})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !reflect.DeepEqual(r.plan, empty("acme/platform", "infra", "", headSHA, true)) {
		t.Fatalf("plan = %+v", r.plan)
	}
	if len(r.differ.ranges) != 0 {
		t.Fatal("initial plan must not compare")
	}
}

func TestBuildCursorAtHead(t *testing.T) {
	r := build(t, "acme/platform", "", fakeCursor{sha: headSHA, found: true}, &fakeDiffer{})
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !reflect.DeepEqual(r.plan, empty("acme/platform", "infra", headSHA, headSHA, false)) {
		t.Fatalf("plan = %+v", r.plan)
	}
}

func TestBuildExplicitTo(t *testing.T) {
	d := &fakeDiffer{changes: []ChangedPath{}}
	r := build(t, "acme/platform", pinnedSHA, fakeCursor{sha: cursorSHA, found: true}, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if d.headCalls != 0 || !reflect.DeepEqual(d.ranges, []string{cursorSHA + "..." + pinnedSHA}) {
		t.Fatalf("head calls %d, ranges %v", d.headCalls, d.ranges)
	}
	if r.plan.To != pinnedSHA {
		t.Fatalf("to = %s", r.plan.To)
	}
}

func TestBuildRouted(t *testing.T) {
	d := &fakeDiffer{changes: []ChangedPath{
		{Path: "deploy/values.yaml", Status: "modified"},
		{Path: "README.md", Status: "modified"},
		{Path: "deploy/secrets/token.yaml", Status: "added"},
		{Path: "legacy/handler.go", Status: "renamed", PreviousPath: "src/api/handler.go"},
		{Path: "billing/invoice.go", Status: "added"},
		{Path: "old/x.go", Status: "removed"},
		{Path: "deploy/api.yaml", Status: "modified"},
	}}
	r := build(t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	want := &Plan{
		Version: 1, Source: "acme/platform", Bundle: "infra", From: cursorSHA, To: headSHA,
		ChangedPaths: []ChangedPath{
			{Path: "README.md", Status: "modified"},
			{Path: "billing/invoice.go", Status: "added"},
			{Path: "deploy/api.yaml", Status: "modified"},
			{Path: "deploy/secrets/token.yaml", Status: "added"},
			{Path: "deploy/values.yaml", Status: "modified"},
			{Path: "legacy/handler.go", Status: "renamed", PreviousPath: "src/api/handler.go"},
			{Path: "old/x.go", Status: "removed"},
		},
		Pages: []Page{
			{Path: "infra/pipelines/deploy.md", State: StateUpdate, SourcePaths: []string{"deploy/api.yaml", "deploy/values.yaml"}},
			{Path: "infra/services/api.md", State: StateUpdate, SourcePaths: []string{"deploy/api.yaml", "src/api/handler.go"}},
			{Path: "infra/services/billing.md", State: StateCreate, Type: "Service", SourcePaths: []string{"billing/invoice.go"}},
			{Path: "infra/services/legacy.md", State: StateMissing, SourcePaths: []string{"old/x.go"}},
		},
		Unrouted: []string{"README.md", "legacy/handler.go"},
		Excluded: []string{"deploy/secrets/token.yaml"},
	}
	if !reflect.DeepEqual(r.plan, want) {
		t.Fatalf("got  %+v\nwant %+v", r.plan, want)
	}
}

func TestBuildNothingRouted(t *testing.T) {
	d := &fakeDiffer{changes: []ChangedPath{{Path: "b.txt", Status: "modified"}, {Path: "a.txt", Status: "added"}}}
	r := build(t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if len(r.plan.Pages) != 0 || !reflect.DeepEqual(r.plan.Unrouted, []string{"a.txt", "b.txt"}) {
		t.Fatalf("plan = %+v", r.plan)
	}
}

func TestBuildUnroutedLoggedOncePerPath(t *testing.T) {
	d := &fakeDiffer{changes: []ChangedPath{
		{Path: "a.txt", Status: "added"},
		{Path: "b.txt", Status: "renamed", PreviousPath: "a.txt"},
		{Path: "deploy/x.yaml", Status: "modified"},
	}}
	r := build(t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, d)
	if r.err != nil {
		t.Fatal(r.err)
	}
	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(r.logs), "\n") {
		if strings.Contains(l, "unrouted path") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 unrouted lines, got %d:\n%s", len(lines), r.logs)
	}
	for i, p := range []string{"a.txt", "b.txt"} {
		if !strings.Contains(lines[i], "level=INFO") || !strings.Contains(lines[i], "source=acme/platform") || !strings.Contains(lines[i], "path="+p) {
			t.Errorf("line %q", lines[i])
		}
	}
}

func TestBuildErrors(t *testing.T) {
	boom := errors.New("boom")
	if r := build(t, "acme/platform", "", fakeCursor{err: boom}, &fakeDiffer{}); !errors.Is(r.err, boom) {
		t.Fatalf("cursor error: %v", r.err)
	}
	r := build(t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, &fakeDiffer{err: ErrDiverged})
	if !errors.Is(r.err, ErrDiverged) || r.plan != nil || !strings.Contains(r.err.Error(), "reconcile required") {
		t.Fatalf("diverged: %v %+v", r.err, r.plan)
	}
}

func TestEncodeGolden(t *testing.T) {
	r := build(t, "acme/platform", "", fakeCursor{sha: cursorSHA, found: true}, &fakeDiffer{changes: []ChangedPath{
		{Path: "deploy/values.yaml", Status: "modified"},
		{Path: "README.md", Status: "modified"},
		{Path: "deploy/secrets/<token>&.yaml", Status: "added"},
	}})
	if r.err != nil {
		t.Fatal(r.err)
	}
	var first bytes.Buffer
	if err := Encode(&first, r.plan); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		var again bytes.Buffer
		if err := Encode(&again, r.plan); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Bytes(), again.Bytes()) {
			t.Fatal("encoding is not deterministic")
		}
	}
	const golden = "testdata/plan.golden.json"
	if *update {
		if err := os.WriteFile(golden, first.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), want) {
		t.Fatalf("got:\n%s\nwant:\n%s", first.Bytes(), want)
	}
}
