package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"

	"github.com/gjed/okf-operator/api/v1alpha1"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

const (
	runCursor   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	runBase     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writerKey   = "sk-writer-model-key-0123456789"
	webhookSec  = "webhook-secret-value-0123456789"
	runNewPage  = "---\ntype: concept\n---\n# API\n\nPackage a.\n"
	runRouting  = "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n      - page: product/api.md\n        paths: [\"src/**\"]\n"
	runOldPage  = "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + runBase + "\nsource_paths: [src/a.go]\n---\n# API\n\nPackage old.\n"
	runPagePath = "product/api.md"
)

// fakeCheckout copies a commit's files out of the GitHub fake, which serves
// no tarballs.
type fakeCheckout struct{ fake *githubtest.Fake }

func (f fakeCheckout) Checkout(_ context.Context, _ github.Clients, repo, sha, dir string) error {
	for name, content := range f.fake.Files(repo, sha) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

type modelStub struct {
	*httptest.Server
	mu    sync.Mutex
	calls int
}

func newModel(t *testing.T, reply func(r *http.Request) (int, string)) *modelStub {
	t.Helper()
	m := &modelStub{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		status, body := reply(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(m.Close)
	return m
}

func pageReply(*http.Request) (int, string) {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"message": map[string]string{"role": "assistant", "content": runNewPage}, "finish_reason": "stop",
	}}})
	return http.StatusOK, string(b)
}

// newRunFixture is source acme/api, whose cursor on acme/kb's kb-state is
// runCursor and diverged, and a KnowledgeBase whose writer model is m. The
// Source was created created ago.
func newRunFixture(t *testing.T, m *modelStub, created time.Duration) (*githubtest.Fake, string, string, *SourceReconciler, *syncBuffer, *syncBuffer) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetFile("acme/api", "main", "src/a.go", "package a\n")
	head := fake.Ref("acme/api", "main")
	fake.SetRepo("acme/api", "main", head)
	fake.SetCompare("acme/api", runCursor, head, []byte(`{"status":"diverged","files":[]}`))
	fake.SetCompare("acme/api", runBase, head, []byte(`{"status":"ahead","files":[
		{"filename":"src/a.go","status":"modified","changes":2,"patch":"@@ -1 +1 @@\n-package old\n+package a\n"}]}`))
	fake.SetRepo("acme/kb", "main", "")
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", runCursor+"\n")
	fake.SetFile("acme/kb", "main", ".kb/routing.yaml", runRouting)
	fake.SetFile("acme/kb", "main", runPagePath, runOldPage)

	_, pem := githubtest.WriteKey(t)
	_, reviewPEM := githubtest.WriteKey(t)
	kb := knowledgeBase("kb", "acme/kb")
	kb.Spec.Writer.Model.BaseURL = m.URL + "/v1"
	kb.Spec.Reviewer.Model.BaseURL = m.URL + "/v1"
	now := time.Now()
	c := newClient(t, kb, newSource("api", "acme/api", "kb", now.Add(-created)),
		secret("kb-writer", map[string]string{v1alpha1.SecretKeyAppID: "7", v1alpha1.SecretKeyPrivateKey: pem}),
		secret("kb-writer-key", map[string]string{v1alpha1.SecretKeyAPIKey: writerKey}),
		secret("kb-review", map[string]string{v1alpha1.SecretKeyAppID: "8", v1alpha1.SecretKeyPrivateKey: reviewPEM}),
		secret("kb-webhook", map[string]string{v1alpha1.SecretKeyWebhook: webhookSec}),
	)
	runLogs := &syncBuffer{}
	runner := &Runner{Secrets: c, APIURL: fake.APIURL(), Checkout: fakeCheckout{fake}, ToolVersion: "test",
		Logger: testLogger(runLogs), Now: time.Now, TempDir: t.TempDir()}
	r, _, logs := newReconciler(c, runner, now)
	return fake, head, pem, r, logs, runLogs
}

func cursorOf(fake *githubtest.Fake) string {
	return fake.Files("acme/kb", "kb-state")["cursors/acme/api"]
}

// The weekly reconcile run goes through checkout, reconcile, write and
// propose in-process, re-anchors the diverged cursor after the PR, and stops
// before gate: a reconcile PR always goes to a human.
func TestRunnerReconcileOpensPRAndStopsBeforeGate(t *testing.T) {
	m := newModel(t, pageReply)
	fake, head, _, r, _, runLogs := newRunFixture(t, m, 8*24*time.Hour)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	st := getSource(t, r, "api").Status
	if st.LastError != "" || st.LastRunMode != v1alpha1.ModeReconcile || st.LastPR == nil || st.LastCursor != head {
		t.Fatalf("status = %+v\nlogs:\n%s", st, runLogs.String())
	}
	if c := cursorOf(fake); c != head+"\n" {
		t.Fatalf("cursor = %q, want the source head", c)
	}
	if got := fake.Files("acme/kb", "okf/acme/api")[runPagePath]; !strings.Contains(got, "Package a.") {
		t.Fatalf("bot branch page = %q", got)
	}
	for _, req := range fake.Requests() {
		if strings.Contains(req, "/files") || strings.Contains(req, "/reviews") {
			t.Errorf("reconcile run reached the gate or review: %s", req)
		}
	}
	if !r.Tracker.IsBot("team/kb", "okf-writer[bot]") {
		t.Error("writer bot login not recorded for the webhook filter")
	}
	for _, auth := range fake.Auths() {
		// The review App (ID 8) signs JWTs with "iss":"8"; none may be signed here.
		if strings.HasPrefix(auth, "Bearer ey") && strings.Contains(decodeJWTIssuer(t, auth), `"iss":"8"`) {
			t.Error("the review App was used in a reconcile run")
		}
	}
}

func decodeSegment(seg string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(seg) }

func keyOf(name string) types.NamespacedName { return types.NamespacedName{Namespace: ns, Name: name} }

func drainEvents(r *SourceReconciler) []string {
	rec := r.Recorder.(*events.FakeRecorder)
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func decodeJWTIssuer(t *testing.T, auth string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(auth, "Bearer "), ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := decodeSegment(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A run that fails in write sets the error condition and leaves the cursor
// on kb-state where it was.
func TestRunnerFailedRunLeavesCursor(t *testing.T) {
	m := newModel(t, func(*http.Request) (int, string) { return http.StatusInternalServerError, `{"error":"down"}` })
	fake, _, _, r, _, _ := newRunFixture(t, m, 8*24*time.Hour)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	st := getSource(t, r, "api").Status
	if !strings.HasPrefix(st.LastError, "write: ") || st.LastReconcileTime != nil {
		t.Fatalf("status = %+v", st)
	}
	if c := cursorOf(fake); c != runCursor+"\n" {
		t.Fatalf("cursor = %q, want it untouched", c)
	}
	if len(fake.PullRequests("acme/kb")) != 0 {
		t.Fatal("a failed run opened a PR")
	}
}

func TestRunnerMissingSecretFailsBeforeGitHub(t *testing.T) {
	m := newModel(t, pageReply)
	fake, _, _, r, _, _ := newRunFixture(t, m, 0)
	kb := &v1alpha1.KnowledgeBase{}
	if err := r.Client.Get(context.Background(), keyOf("kb"), kb); err != nil {
		t.Fatal(err)
	}
	kb.Spec.Writer.AppSecretRef.Name = "missing"
	if err := r.Client.Update(context.Background(), kb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; !strings.HasPrefix(st.LastError, "credentials: read Secret missing") {
		t.Fatalf("status = %+v", st)
	}
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("%d GitHub requests before credentials loaded", n)
	}
}

// App keys, JWTs, installation tokens, the webhook secret and model keys
// never reach logs, events, status or spans, even when GitHub and the model
// echo them back in errors.
func TestSecretsNeverLeak(t *testing.T) {
	m := newModel(t, func(r *http.Request) (int, string) {
		return http.StatusUnauthorized, `{"error":"bad key ` + r.Header.Get("Authorization") + `"}`
	})
	fake, _, pem, r, logs, runLogs := newRunFixture(t, m, 8*24*time.Hour)
	spans.Reset()
	fake.EchoAuth = true

	// Run 1 fails at GitHub with the installation token in the error.
	fake.FailStatus["GET /repos/acme/kb/pulls"] = http.StatusBadGateway
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	status1, _ := json.Marshal(getSource(t, r, "api").Status)
	if !strings.Contains(string(status1), "REDACTED") {
		t.Fatalf("run 1 did not fail with an echoed token: %s", status1)
	}
	// Run 2 fails at the model with the model key in the error.
	delete(fake.FailStatus, "GET /repos/acme/kb/pulls")
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	status2, _ := json.Marshal(getSource(t, r, "api").Status)
	if !strings.Contains(string(status2), "write: ") {
		t.Fatalf("run 2 did not fail in write: %s", status2)
	}

	jwt := ""
	if reqs := fake.TokenRequests(); len(reqs) > 0 {
		jwt = strings.TrimPrefix(reqs[0].Auth, "Bearer ")
	}
	secrets := map[string]string{
		"installation token": fake.InstallationToken,
		"model key":          writerKey,
		"webhook secret":     webhookSec,
		"pem line":           strings.Split(pem, "\n")[3],
		"jwt":                jwt,
	}
	var spanText strings.Builder
	for _, s := range spans.Ended() {
		spanText.WriteString(s.Name() + " " + s.Status().Description + "\n")
		for _, a := range s.Attributes() {
			spanText.WriteString(string(a.Key) + "=" + a.Value.String() + "\n")
		}
		for _, e := range s.Events() {
			spanText.WriteString(e.Name + "\n")
			for _, a := range e.Attributes {
				spanText.WriteString(a.Value.String() + "\n")
			}
		}
	}
	if !strings.Contains(spanText.String(), "controller run") {
		t.Fatal("no controller run span recorded")
	}
	sinks := map[string]string{
		"logs":   logs.String() + runLogs.String(),
		"events": strings.Join(drainEvents(r), "\n"),
		"status": string(status1) + string(status2),
		"spans":  spanText.String(),
	}
	for sink, text := range sinks {
		for name, s := range secrets {
			if s != "" && strings.Contains(text, s) {
				t.Errorf("%s leaked into %s", name, sink)
			}
		}
	}
}

// A run that pushes nothing gates the open bot PR's head again when the
// controller remembers the plan behind that head, and leaves it alone
// otherwise.
func TestRunnerRegatesAnUnchangedHead(t *testing.T) {
	m := newModel(t, pageReply)
	fake, head, _, r, _, runLogs := newRunFixture(t, m, 0)
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", head+"\n")
	fake.SetFile("acme/kb", "okf/acme/api", runPagePath, runOldPage+"\nbot edit\n")
	n := fake.AddPullRequest("acme/kb", githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "open"})
	gatePath := "/repos/acme/kb/pulls/" + strconv.Itoa(n)
	gated := func() bool {
		for _, req := range fake.Requests() {
			if strings.HasSuffix(req, gatePath) {
				return true
			}
		}
		return false
	}

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; st.LastError != "" || gated() {
		t.Fatalf("status %+v, gated %v without a remembered plan\n%s", st, gated(), runLogs.String())
	}

	runner := r.Pipeline.(*Runner)
	runner.gated.put("acme/kb acme/api", fake.Ref("acme/kb", "okf/acme/api"), &plan.Plan{Version: plan.Version, Source: "acme/api"})
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	// The fake serves no PR for the gate, so the gate fails the run; that it
	// ran at all is the point.
	if st := getSource(t, r, "api").Status; !strings.HasPrefix(st.LastError, "gate: ") || !gated() {
		t.Fatalf("status %+v, gated %v", st, gated())
	}
}
