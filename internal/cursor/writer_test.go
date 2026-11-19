package cursor

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const (
	sha2 = "1111111111111111111111111111111111111111"
	sha3 = "2222222222222222222222222222222222222222"
)

func writer(t *testing.T, fake *githubtest.Fake) Writer {
	t.Helper()
	p, err := github.NewProvider(github.Config{APIURL: fake.APIURL(), Token: "ghp_test0123456789"},
		github.Access{Repos: []string{"acme/kb"}, Permissions: gogithub.InstallationPermissions{Contents: new("write")}},
		&github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return Writer{Clients: p, KnowledgeRepo: "acme/kb"}
}

func TestAdvance(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(f *githubtest.Fake)
		from     string
		initial  bool
		wantErr  bool
		want     string // cursor content after the call
		wantRoot bool   // kb-state head is a parentless commit
	}{
		{"advance", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/api", sha+"\n") }, sha, false, false, sha2 + "\n", false},
		{"initial on existing branch", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/other", sha+"\n") }, "", true, false, sha2 + "\n", false},
		{"kb-state missing", func(f *githubtest.Fake) { f.SetFile("acme/kb", "main", "README.md", "x") }, "", true, false, sha2 + "\n", true},
		{"cursor not at from", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/api", sha3+"\n") }, sha, false, true, sha3 + "\n", false},
		{"initial but cursor exists", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/api", sha3+"\n") }, "", true, true, sha3 + "\n", false},
		{"cursor missing but plan has from", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/other", sha+"\n") }, sha, false, true, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := githubtest.New(t)
			tt.setup(fake)
			err := writer(t, fake).Advance(t.Context(), "acme/api", tt.from, sha2, tt.initial)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrMoved) {
				t.Fatalf("err = %v, want ErrMoved", err)
			}
			files := fake.Files("acme/kb", Branch)
			if got := files["cursors/acme/api"]; got != tt.want {
				t.Fatalf("cursor = %q, want %q", got, tt.want)
			}
			if tt.wantRoot {
				head := fake.Ref("acme/kb", Branch)
				if len(fake.Parents("acme/kb", head)) != 0 || len(files) != 1 {
					t.Fatalf("kb-state head has parents %v and files %v, want an orphan with the cursor only", fake.Parents("acme/kb", head), files)
				}
			}
			if tt.wantErr && slices.ContainsFunc(fake.Requests(), func(r string) bool { return strings.HasPrefix(r, "PUT ") }) {
				t.Fatalf("wrote despite a moved cursor: %v", fake.Requests())
			}
		})
	}
}

func TestAdvanceNoOpWhenFromEqualsTo(t *testing.T) {
	fake := githubtest.New(t)
	fake.SetFile("acme/kb", Branch, "cursors/acme/api", sha2+"\n")
	if err := writer(t, fake).Advance(t.Context(), "acme/api", sha2, sha2, false); err != nil {
		t.Fatal(err)
	}
	if w := fake.Writes(); len(w) != 0 {
		t.Fatalf("writes = %v, want none", w)
	}
}

// Another run advances the cursor after Advance read it: the stale blob SHA
// makes the write fail with 409 and the other run's value stays.
func TestAdvanceLosesRace(t *testing.T) {
	fake := githubtest.New(t)
	fake.SetFile("acme/kb", Branch, "cursors/acme/api", sha+"\n")
	var done atomic.Bool
	fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodPut && done.CompareAndSwap(false, true) {
			fake.SetFile("acme/kb", Branch, "cursors/acme/api", sha3+"\n")
		}
	})
	err := writer(t, fake).Advance(t.Context(), "acme/api", sha, sha2, false)
	if !errors.Is(err, ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if got := fake.Files("acme/kb", Branch)["cursors/acme/api"]; got != sha3+"\n" {
		t.Fatalf("cursor = %q, want the other run's value", got)
	}
}

// kb-state appears between the read and the orphan ref creation: 422.
func TestAdvanceBranchCreatedConcurrently(t *testing.T) {
	fake := githubtest.New(t)
	var done atomic.Bool
	fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/repos/acme/kb/git/refs" && done.CompareAndSwap(false, true) {
			fake.SetFile("acme/kb", Branch, "cursors/acme/api", sha3+"\n")
		}
	})
	err := writer(t, fake).Advance(t.Context(), "acme/api", "", sha2, true)
	if !errors.Is(err, ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if got := fake.Files("acme/kb", Branch)["cursors/acme/api"]; got != sha3+"\n" {
		t.Fatalf("cursor = %q, want the other run's value", got)
	}
}

func TestCheckWritesNothing(t *testing.T) {
	fake := githubtest.New(t)
	fake.SetFile("acme/kb", Branch, "cursors/acme/api", sha+"\n")
	w := writer(t, fake)
	if err := w.Check(t.Context(), "acme/api", sha, false); err != nil {
		t.Fatal(err)
	}
	if err := w.Check(t.Context(), "acme/api", sha2, false); !errors.Is(err, ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if w := fake.Writes(); len(w) != 0 {
		t.Fatalf("writes = %v, want none", w)
	}
}
