package controller

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

func tokenClients(t *testing.T, apiURL string) github.Clients {
	t.Helper()
	p, err := github.NewProvider(github.Config{APIURL: apiURL, Token: "ghp_test0123456789"}, github.Access{}, &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecordDeclined(t *testing.T) {
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	fake := githubtest.New(t)
	fake.SetFile("acme/kb", "main", "README.md", "kb\n")
	fake.SetFile("acme/kb", "okf/acme/api", "README.md", "kb\n")
	declinedPR := fake.AddPullRequest("acme/kb", githubtest.PullRequest{
		Head: "okf/acme/api", Base: "main", State: "closed",
		Body: "intro\n\n<!-- okf:range " + a + ".." + b + " -->\n### one\n\n<!-- okf:range " + b + ".." + c + " -->\n### two\n",
	})
	fake.AddPullRequest("acme/kb", githubtest.PullRequest{
		Head: "okf/acme/api", Base: "main", State: "closed", MergedAt: "2026-11-20T10:00:00Z",
		Body: "<!-- okf:range " + c + ".." + a + " -->\n",
	})
	fake.AddPullRequest("acme/kb", githubtest.PullRequest{Head: "okf/acme/web", Base: "main", State: "closed",
		Body: "<!-- okf:range .." + a + " -->\n"})
	fake.AddPullRequest("acme/kb", githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "open",
		Body: "<!-- okf:range " + a + ".." + c + " -->\n"})
	clients := tokenClients(t, fake.APIURL())
	ctx := context.Background()

	// No kb-state yet: the first record creates it.
	n, err := recordDeclined(ctx, clients, "acme/kb", "acme/api")
	if err != nil || n != 1 {
		t.Fatalf("recorded %d, %v", n, err)
	}
	want := strings.Join([]string{
		strconv.Itoa(declinedPR) + " " + a + " " + b,
		strconv.Itoa(declinedPR) + " " + b + " " + c,
	}, "\n") + "\n"
	if got := fake.Files("acme/kb", "kb-state")["declined/acme/api"]; got != want {
		t.Fatalf("declined file = %q, want %q", got, want)
	}
	if _, ok := fake.Files("acme/kb", "kb-state")["cursors/acme/api"]; ok {
		t.Fatal("declined bookkeeping wrote a cursor")
	}
	ref := fake.Ref("acme/kb", "kb-state")
	if n, err := recordDeclined(ctx, clients, "acme/kb", "acme/api"); err != nil || n != 0 || fake.Ref("acme/kb", "kb-state") != ref {
		t.Fatalf("second record: %d, %v; kb-state moved: %v", n, err, fake.Ref("acme/kb", "kb-state") != ref)
	}

	// A later declined PR with an initial range is appended next to a cursor.
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", a+"\n")
	later := fake.AddPullRequest("acme/kb", githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "closed",
		Body: "<!-- okf:range .." + c + " -->\n"})
	if n, err := recordDeclined(ctx, clients, "acme/kb", "acme/api"); err != nil || n != 1 {
		t.Fatalf("third record: %d, %v", n, err)
	}
	files := fake.Files("acme/kb", "kb-state")
	if got := files["declined/acme/api"]; got != want+strconv.Itoa(later)+" - "+c+"\n" {
		t.Fatalf("declined file = %q", got)
	}
	if files["cursors/acme/api"] != a+"\n" {
		t.Fatal("cursor changed")
	}
}

// reviewsServer serves one PR's reviews and records dismissals.
type reviewsServer struct {
	*httptest.Server
	mu        sync.Mutex
	dismissed []string
}

func newReviewsServer(t *testing.T, reviews string) *reviewsServer {
	t.Helper()
	s := &reviewsServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/kb/pulls/5/reviews", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reviews))
	})
	mux.HandleFunc("PUT /repos/acme/kb/pulls/5/reviews/{id}/dismissals", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Message string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.dismissed = append(s.dismissed, r.PathValue("id")+": "+body.Message)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"state":"DISMISSED"}`))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestDismissStaleApprovesOfTheFailedHead(t *testing.T) {
	head, other := strings.Repeat("4", 40), strings.Repeat("5", 40)
	srv := newReviewsServer(t, `[
		{"id":1,"state":"APPROVED","commit_id":"`+head+`","user":{"login":"okf-review[bot]"}},
		{"id":2,"state":"APPROVED","commit_id":"`+head+`","user":{"login":"maintainer"}},
		{"id":3,"state":"APPROVED","commit_id":"`+other+`","user":{"login":"okf-review[bot]"}},
		{"id":4,"state":"DISMISSED","commit_id":"`+head+`","user":{"login":"okf-review[bot]"}},
		{"id":5,"state":"COMMENTED","commit_id":"`+head+`","user":{"login":"okf-review[bot]"}}]`)
	n, err := dismissStale(context.Background(), tokenClients(t, srv.URL+"/"), "acme/kb", 5, head, "okf-review[bot]")
	if err != nil || n != 1 {
		t.Fatalf("dismissed %d, %v", n, err)
	}
	if len(srv.dismissed) != 1 || !strings.HasPrefix(srv.dismissed[0], "1: okf-operator: a later gate run failed at "+head) {
		t.Fatalf("dismissals = %v", srv.dismissed)
	}
}

// fakeStages scripts the gate results and records the calls.
type fakeStages struct {
	gates []bool // pass of each Gate call
	calls []string
	head  string
}

func (f *fakeStages) Gate(_ context.Context, rv *gate.Review) (*gate.Report, error) {
	pass := f.gates[0]
	f.gates = f.gates[1:]
	f.calls = append(f.calls, "gate"+map[bool]string{false: "", true: "+review"}[rv != nil])
	return &gate.Report{HeadSHA: f.head, Pass: pass}, nil
}

func (f *fakeStages) Review(context.Context) (*gate.Review, error) {
	f.calls = append(f.calls, "review")
	return &gate.Review{HeadSHA: f.head, Pass: true}, nil
}

func (f *fakeStages) Approve(context.Context, *gate.Report) (bool, error) {
	f.calls = append(f.calls, "approve")
	return true, nil
}

func (f *fakeStages) Dismiss(_ context.Context, head string) (int, error) {
	f.calls = append(f.calls, "dismiss "+head)
	return 1, nil
}

func TestGateAndApproveOrder(t *testing.T) {
	head := strings.Repeat("4", 40)
	tests := []struct {
		name      string
		gates     []bool
		want      []string
		approved  bool
		dismissed int
	}{
		{"deterministic gate fails: no review, approval of the head dismissed", []bool{false}, []string{"gate", "dismiss " + head}, false, 1},
		{"review gate fails on an approved head: approval dismissed", []bool{true, false}, []string{"gate", "review", "gate+review", "dismiss " + head}, false, 1},
		{"every gate passes: approved", []bool{true, true}, []string{"gate", "review", "gate+review", "approve"}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeStages{gates: tt.gates, head: head}
			var out Outcome
			if err := gateAndApprove(context.Background(), s, &out); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(s.calls, tt.want) || out.Approved != tt.approved || out.Dismissed != tt.dismissed {
				t.Fatalf("calls %v, outcome %+v", s.calls, out)
			}
		})
	}
}
