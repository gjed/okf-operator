package cursor

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func reader(t *testing.T, fake *githubtest.Fake) Reader {
	t.Helper()
	p, err := github.NewProvider(github.Config{APIURL: fake.APIURL(), Token: "ghp_test0123456789"},
		github.ReadContents("acme/kb"), &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return Reader{Clients: p, KnowledgeRepo: "acme/kb"}
}

func TestRead(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(f *githubtest.Fake)
		wantSHA   string
		wantFound bool
		wantErr   string
	}{
		{"found", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/api", sha+"\n") }, sha, true, ""},
		{"file 404", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/other", sha+"\n") }, "", false, ""},
		{"branch 404", func(*githubtest.Fake) {}, "", false, ""},
		{"wrong branch only", func(f *githubtest.Fake) { f.SetFile("acme/kb", "main", "cursors/acme/api", sha+"\n") }, "", false, ""},
		{"malformed", func(f *githubtest.Fake) { f.SetFile("acme/kb", Branch, "cursors/acme/api", "abc123\n") }, "", false,
			"cursor cursors/acme/api on acme/kb@kb-state is malformed"},
		{"uppercase sha", func(f *githubtest.Fake) {
			f.SetFile("acme/kb", Branch, "cursors/acme/api", strings.ToUpper(sha)+"\n")
		}, "", false, "malformed"},
		{"server error", func(f *githubtest.Fake) {
			f.FailStatus["/repos/acme/kb/contents/cursors/acme/api"] = http.StatusInternalServerError
		}, "", false, "500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := githubtest.New(t)
			tt.setup(fake)
			got, found, err := reader(t, fake).Read(context.Background(), "acme/api")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantSHA || found != tt.wantFound {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, found, tt.wantSHA, tt.wantFound)
			}
		})
	}
}
