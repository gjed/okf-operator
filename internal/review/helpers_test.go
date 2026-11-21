package review

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
)

const (
	kbRepo   = "acme/kb"
	srcRepo  = "acme/api"
	prNumber = 7
	baseSHA  = "1111111111111111111111111111111111111111"
	headSHA  = "2222222222222222222222222222222222222222"
	fromSHA  = "4444444444444444444444444444444444444444"
	toSHA    = "3333333333333333333333333333333333333333"
	apiPath  = "product/api.md"
	apiPatch = "@@ -1 +1 @@\n-const rateLimit = 5\n+const rateLimit = 10\n"
)

func page(sha, updated, body string) string {
	return "---\ntype: Service\nsource_repo: acme/api\nsource_sha: " + sha +
		"\nsource_paths: [api/x.go]\nupdated_at: " + updated + "\ngenerated_by: okf-operator/dev/m1\n---\n# API\n\n" + body + "\n"
}

var (
	apiBefore = page(fromSHA, "2026-11-17T10:00:00Z", "Rate limit is 5 rps.")
	apiAfter  = page(toSHA, "2026-11-18T10:00:00Z", "Rate limit is 10 rps.")
)

// fixture is PR 7 of acme/kb modifying product/api.md, planned from
// api/x.go of acme/api, with a compare of fromSHA...toSHA that has its patch.
type fixture struct {
	fake  *githubtest.Fake
	plan  *plan.Plan
	files []githubtest.PullFile
	model *modelStub
	max   int
}

func newFixture(t *testing.T, reply func(*http.Request) (int, string)) *fixture {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetRepo(kbRepo, "main", baseSHA)
	fake.SetFile(kbRepo, baseSHA, apiPath, apiBefore)
	fake.SetFile(kbRepo, headSHA, apiPath, apiAfter)
	fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main"})
	fake.SetCompare(srcRepo, fromSHA, toSHA, compareBody(map[string]string{"api/x.go": apiPatch}))
	return &fixture{
		fake: fake,
		plan: &plan.Plan{Version: plan.Version, Source: srcRepo, Bundle: "product", From: fromSHA, To: toSHA,
			Pages: []plan.Page{{Path: apiPath, State: plan.StateUpdate, SourcePaths: []string{"api/x.go"}}}},
		files: []githubtest.PullFile{{Filename: apiPath, Status: "modified", Additions: 1, Deletions: 1}},
		model: newModelStub(t, reply),
		max:   16000,
	}
}

// compareBody is a compare API response listing one modified file per patch.
func compareBody(patches map[string]string) []byte {
	type file struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
		Patch    string `json:"patch"`
		Changes  int    `json:"changes"`
	}
	files := []file{}
	for name, patch := range patches {
		files = append(files, file{Filename: name, Status: "modified", Patch: patch, Changes: 2})
	}
	body, _ := json.Marshal(map[string]any{"status": "ahead", "files": files, "commits": []any{map[string]string{"sha": toSHA}}})
	return body
}

func (f *fixture) run(t *testing.T) (*Result, error) {
	t.Helper()
	return f.runCtx(context.Background(), t)
}

func (f *fixture) runCtx(ctx context.Context, t *testing.T) (*Result, error) {
	t.Helper()
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider, err := github.NewProvider(github.Config{APIURL: f.fake.APIURL(), Token: "test-token"},
		github.ReadContents(kbRepo, srcRepo), &github.Secrets{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	return Run(ctx, Deps{
		Reader:           gate.GitHubReader{Clients: provider},
		Source:           plan.GitHubDiffer{Clients: provider},
		Model:            &model.Client{BaseURL: f.model.URL, Model: "reviewer"},
		ModelName:        "reviewer",
		MaxTokensPerPage: f.max,
		Logger:           logger,
	}, kbRepo, prNumber, f.plan)
}

// modelStub is an OpenAI-compatible endpoint that records each user message.
type modelStub struct {
	*httptest.Server
	mu    sync.Mutex
	users []string
}

func newModelStub(t *testing.T, reply func(*http.Request) (int, string)) *modelStub {
	t.Helper()
	m := &modelStub{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		m.mu.Lock()
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				m.users = append(m.users, msg.Content)
			}
		}
		m.mu.Unlock()
		status, out := reply(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *modelStub) prompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.users...)
}

// answer replies with content as the assistant message.
func answer(content string) func(*http.Request) (int, string) {
	return answerWith(content, "stop")
}

func answerWith(content, finish string) func(*http.Request) (int, string) {
	return func(*http.Request) (int, string) {
		c, _ := json.Marshal(content)
		return http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%s},"finish_reason":%q}],
			"usage":{"prompt_tokens":100,"completion_tokens":10}}`, c, finish)
	}
}

const supported = `{"verdict":"supported","unsupported":[]}`

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("does not contain %q:\n%s", sub, s)
	}
}
