package propose

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

const (
	humanA   = "---\ntype: concept\n---\n# A\n\nEdited by a human.\n"
	botA     = "---\ntype: concept\n---\n# A\n\nBot.\n"
	botB     = "---\ntype: concept\n---\n# B\n\nBot.\n"
	defC     = "---\ntype: concept\n---\n# C\n\nDefault.\n"
	regenA   = "---\ntype: concept\n---\n# A\n\nHuman text and bot range.\n"
	testMid  = commit1
	regenMsg = "okf: merge main into okf/acme/api\n"
)

func marker(from, to string) string {
	return markerPrefix + from + ".." + to + " -->\n### acme/api `" + short(from) + ".." + short(to) + "`\n\nPages:\n\n- changed: `docs/a.md`\n\n"
}

// twoRanges is the body of a bot PR that two runs updated.
func twoRanges() string {
	return "Knowledge updates.\n\n" + marker(testFrom, testMid) + marker(testMid, testTo)
}

type regenCall struct {
	conflicts []string
	from, to  string
}

// conflictFixture is a dirty bot PR: the bot branch changed docs/a.md and
// docs/b.md, the default branch changed docs/a.md and docs/c.md since.
type conflictFixture struct {
	*fixture
	pull    *Pull
	defHead string
	calls   []regenCall
}

func newConflictFixture(t *testing.T) *conflictFixture {
	t.Helper()
	f := newFixture(t)
	pull := f.botPR(t, twoRanges(), map[string]string{"docs/a.md": botA, "docs/b.md": botB}, "dirty")
	f.fake.SetFile(testKB, "main", "docs/a.md", humanA)
	f.fake.SetFile(testKB, "main", "docs/c.md", defC)
	return &conflictFixture{fixture: f, pull: pull, defHead: f.fake.Ref(testKB, "main")}
}

// regenerate stands for write: it leaves the default branch checkout in
// f.dir with docs/a.md replaced by content, unless content is empty.
func (c *conflictFixture) regenerate(t *testing.T, result, content string) RegenerateFunc {
	t.Helper()
	return func(_ context.Context, conflicts []string, from, to string) (*Regenerated, error) {
		c.calls = append(c.calls, regenCall{conflicts, from, to})
		files := c.fake.Files(testKB, c.defHead)
		if content != "" {
			files["docs/a.md"] = content
		}
		c.checkout(t, files)
		var pages []plan.Page
		var results []write.PageResult
		for _, p := range conflicts {
			pages = append(pages, plan.Page{Path: p, State: plan.StateUpdate, Type: "concept", SourcePaths: []string{"src/a.go"}})
			results = append(results, write.PageResult{Path: p, Result: result, Attempts: 1})
		}
		sub := &plan.Plan{Version: plan.Version, Source: testSource, Bundle: testBundle, From: from, To: to, Pages: pages}
		full := *sub
		full.Pages = append([]plan.Page{{Path: "docs/b.md", State: plan.StateUpdate, Type: "concept"}}, pages...)
		return &Regenerated{
			Plan: sub, Full: &full, Dir: c.dir,
			Report: &write.Report{Version: write.Version, Source: testSource, Bundle: testBundle, From: from, To: to, Pages: results},
		}, nil
	}
}

func (c *conflictFixture) repair(t *testing.T, regen RegenerateFunc) (*Repaired, error) {
	t.Helper()
	return Repair(t.Context(), RepairInput{
		Knowledge: c.knowledge(t), Source: testSource, Pull: c.pull,
		DefBranch: "main", DefHead: c.defHead, Regenerate: regen, Logger: slog.New(slog.DiscardHandler),
	})
}

func TestRepairRegeneratesOnlyConflictingPages(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(sdkmetric.NewMeterProvider()) })

	c := newConflictFixture(t)
	got, err := c.repair(t, c.regenerate(t, write.ResultChanged, regenA))
	if err != nil {
		t.Fatal(err)
	}
	// Write ran for the one page both sides changed, over the whole range.
	if want := []regenCall{{[]string{"docs/a.md"}, testFrom, testTo}}; !slices.EqualFunc(c.calls, want, func(a, b regenCall) bool {
		return slices.Equal(a.conflicts, b.conflicts) && a.from == b.from && a.to == b.to
	}) {
		t.Fatalf("regenerate calls = %+v, want %+v", c.calls, want)
	}
	if !slices.Equal(got.Conflicts, []string{"docs/a.md"}) || got.Plan == nil {
		t.Fatalf("repaired = %+v", got)
	}
	// One commit on both parents, and the branch fast-forwarded to it.
	if head := c.fake.Ref(testKB, botBranch); head != got.Head || !slices.Equal(c.fake.Parents(testKB, head), []string{c.defHead, c.pull.HeadSHA}) {
		t.Fatalf("branch %s, repaired head %s, parents %v", head, got.Head, c.fake.Parents(testKB, got.Head))
	}
	want := with(map[string]string{"docs/a.md": regenA, "docs/b.md": botB, "docs/c.md": defC})
	if files := c.fake.Files(testKB, got.Head); !mapsEqual(files, want) {
		t.Fatalf("files = %v", files)
	}
	if msg := c.fake.Message(testKB, got.Head); !strings.HasPrefix(msg, regenMsg) || !strings.Contains(msg, "docs/a.md") {
		t.Errorf("message = %q", msg)
	}
	// The PR stays the same PR, keeps its sections and gains a note.
	pulls := c.fake.PullRequests(testKB)
	if len(pulls) != 1 || pulls[0].Number != c.pull.Number || !strings.HasPrefix(pulls[0].Body, twoRanges()) ||
		!strings.Contains(pulls[0].Body, "### Conflict repair") || !strings.Contains(pulls[0].Body, "- changed: `docs/a.md`\n") {
		t.Fatalf("pulls = %+v", pulls)
	}
	if _, _, err := PRRange(pulls[0].Body); err != nil {
		t.Errorf("the note broke the range markers: %v", err)
	}
	// The cursor is not touched, and nothing but the repair is counted.
	if cur := c.cursor(); cur != testFrom+"\n" {
		t.Fatalf("cursor = %q", cur)
	}
	for _, w := range c.fake.Writes() {
		if strings.Contains(w, "/contents/") {
			t.Errorf("repair wrote the cursor: %s", w)
		}
	}
	if counts := repairCounts(t, reader); counts["okf.propose.repair=regenerate"] != 1 || len(counts) != 1 {
		t.Fatalf("repairs = %v", counts)
	}
}

// A conflicting page the model leaves as it is, or that fails validation,
// keeps the default branch's version.
func TestRepairKeepsDefaultPageWhenWriteDoesNotChangeIt(t *testing.T) {
	for _, result := range []string{write.ResultUnchanged, write.ResultDropped} {
		t.Run(result, func(t *testing.T) {
			c := newConflictFixture(t)
			got, err := c.repair(t, c.regenerate(t, result, ""))
			if err != nil {
				t.Fatal(err)
			}
			files := c.fake.Files(testKB, got.Head)
			if files["docs/a.md"] != humanA || files["docs/b.md"] != botB || files["docs/c.md"] != defC {
				t.Fatalf("files = %v", files)
			}
			if body := c.fake.PullRequests(testKB)[0].Body; !strings.Contains(body, "- "+result+": `docs/a.md` (") ||
				!strings.Contains(body, "the main version was kept)") {
				t.Errorf("body lacks the kept page:\n%s", body)
			}
		})
	}
}

// A dirty PR with no page changed on both sides is merged as it is: no model.
func TestRepairWithoutConflictingPages(t *testing.T) {
	f := newFixture(t)
	pull := f.botPR(t, twoRanges(), map[string]string{"docs/b.md": botB}, "dirty")
	f.fake.SetFile(testKB, "main", "docs/c.md", defC)
	defHead := f.fake.Ref(testKB, "main")
	got, err := Repair(t.Context(), RepairInput{
		Knowledge: f.knowledge(t), Source: testSource, Pull: pull, DefBranch: "main", DefHead: defHead,
		Regenerate: func(context.Context, []string, string, string) (*Regenerated, error) {
			t.Fatal("regenerate called with no conflicting page")
			return nil, nil
		},
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 0 || got.Plan != nil || !slices.Equal(f.fake.Parents(testKB, got.Head), []string{defHead, pull.HeadSHA}) {
		t.Fatalf("repaired = %+v", got)
	}
	if files := f.fake.Files(testKB, got.Head); !mapsEqual(files, with(map[string]string{"docs/b.md": botB, "docs/c.md": defC})) {
		t.Fatalf("files = %v", files)
	}
}

func TestRepairMergesDeletions(t *testing.T) {
	f := newFixture(t)
	pull := f.botPR(t, twoRanges(), map[string]string{"docs/b.md": botB}, "dirty")
	f.fake.DeleteFile(testKB, botBranch, "docs/c.md")
	pull.HeadSHA = f.fake.Ref(testKB, botBranch)
	f.fake.SetFile(testKB, "main", "docs/a.md", humanA)
	got, err := Repair(t.Context(), RepairInput{
		Knowledge: f.knowledge(t), Source: testSource, Pull: pull, DefBranch: "main", DefHead: f.fake.Ref(testKB, "main"),
		Regenerate: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := with(map[string]string{"docs/a.md": humanA, "docs/b.md": botB})
	delete(want, "docs/c.md")
	if files := f.fake.Files(testKB, got.Head); !mapsEqual(files, want) {
		t.Fatalf("files = %v", files)
	}
}

// A repair that cannot proceed writes nothing.
func TestRepairRefusals(t *testing.T) {
	reconcile := markerPrefix + testFrom + ".." + testTo + " -->\n### reconcile acme/api at `2222222`\n\nPages:\n"
	tests := []struct {
		name, body, want string
		regen            func(c *conflictFixture, t *testing.T) RegenerateFunc
	}{
		{"omitted ranges", omittedTag + " 2 earlier ranges omitted, see the commit history.\n\n" + marker(testMid, testTo), "omitted earlier ranges", nil},
		{"reconcile section", twoRanges() + reconcile, "reconcile section", nil},
		{"ranges that do not chain", "x\n\n" + marker(testFrom, testMid) + marker(testTo, testTo), "do not chain", nil},
		{"no range", "just text", "no source range", nil},
		{"conflicting path that is not a page", twoRanges(), "the regeneration plan has pages [docs/zzz.md]", func(c *conflictFixture, t *testing.T) RegenerateFunc {
			return func(ctx context.Context, conflicts []string, from, to string) (*Regenerated, error) {
				rg, err := c.regenerate(t, write.ResultChanged, regenA)(ctx, conflicts, from, to)
				rg.Plan.Pages[0].Path = "docs/zzz.md"
				return rg, err
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newConflictFixture(t)
			c.pull.Body = tt.body
			var regen RegenerateFunc
			if tt.regen != nil {
				regen = tt.regen(c, t)
			}
			_, err := c.repair(t, regen)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "bot PR #") {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			assertNoWrites(t, c.fixture)
			if c.fake.Ref(testKB, botBranch) != c.pull.HeadSHA {
				t.Fatal("the bot branch moved")
			}
		})
	}
}

// A push to the bot branch after the repair read its head is not lost: the
// branch only fast-forwards.
func TestRepairFailsWhenBranchMoved(t *testing.T) {
	c := newConflictFixture(t)
	c.fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/git/refs/") {
			c.fake.SetFile(testKB, botBranch, "docs/b.md", "pushed meanwhile\n")
		}
	})
	_, err := c.repair(t, c.regenerate(t, write.ResultChanged, regenA))
	if err == nil || !strings.Contains(err.Error(), "fast-forward") {
		t.Fatalf("err = %v", err)
	}
	if c.fake.Files(testKB, botBranch)["docs/b.md"] != "pushed meanwhile\n" {
		t.Fatal("the other push was lost")
	}
	if cur := c.cursor(); cur != testFrom+"\n" {
		t.Fatalf("cursor = %q", cur)
	}
}

func TestDiffTrees(t *testing.T) {
	e := func(sha string) Entry { return Entry{Mode: modeFile, SHA: sha} }
	base := map[string]Entry{"a": e("1"), "b": e("1"), "c": e("1"), "d": e("1"), "e": e("1"), "f": e("1")}
	def := map[string]Entry{"a": e("2"), "b": e("1"), "c": e("3"), "d": e("2"), "e": e("1"), "f": e("2"), "g": e("1")}
	head := map[string]Entry{"a": e("4"), "b": e("2"), "c": e("3"), "d": e("1"), "f": e("2"), "h": e("5")}
	// a: both sides changed it, differently. b: the bot only. c, f: both made
	// the same change. d: the default branch only. e: the bot deleted it.
	// g, h: added on one side.
	conflicts, take := diffTrees(base, def, head)
	if !slices.Equal(conflicts, []string{"a"}) {
		t.Errorf("conflicts = %v, want [a]", conflicts)
	}
	got := map[string]string{}
	for p, en := range take {
		if en == nil {
			got[p] = "delete"
		} else {
			got[p] = en.SHA
		}
	}
	if want := (map[string]string{"b": "2", "e": "delete", "h": "5"}); !maps.Equal(got, want) {
		t.Errorf("take = %v, want %v", got, want)
	}
}

func TestDiffTreesDeleteAgainstModify(t *testing.T) {
	e := func(sha string) Entry { return Entry{Mode: modeFile, SHA: sha} }
	base := map[string]Entry{"a": e("1"), "b": e("1")}
	def := map[string]Entry{"b": e("1")}  // default deleted a
	head := map[string]Entry{"a": e("2")} // bot modified a and deleted b
	conflicts, take := diffTrees(base, def, head)
	if en, ok := take["b"]; !slices.Equal(conflicts, []string{"a"}) || len(take) != 1 || !ok || en != nil {
		t.Fatalf("conflicts = %v, take = %v", conflicts, take)
	}
}

func TestPRRange(t *testing.T) {
	from, to, err := PRRange(twoRanges())
	if err != nil || from != testFrom || to != testTo {
		t.Fatalf("range = %s..%s, %v", from, to, err)
	}
	if from, to, err = PRRange("x\n\n" + marker(testMid, testTo)); err != nil || from != testMid || to != testTo {
		t.Fatalf("single range = %s..%s, %v", from, to, err)
	}
}
