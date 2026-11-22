package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/types"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

const (
	secretA = "webhook-secret-of-kb-a"
	secretB = "webhook-secret-of-kb-b"
)

type queueRecorder struct {
	mu  sync.Mutex
	got []string
}

func (q *queueRecorder) add(nn types.NamespacedName) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.got = append(q.got, nn.String())
}

func (q *queueRecorder) items() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Sorted(slices.Values(q.got))
}

// webhookFixture has knowledge bases kb-a (acme/kb-a) and kb-b (acme/kb-b),
// Sources of acme/api in both and a Source of acme/web in kb-a.
func webhookFixture(t *testing.T) (*Webhook, *queueRecorder) {
	t.Helper()
	now := time.Now()
	c := newClient(t,
		knowledgeBase("kb-a", "acme/kb-a"), knowledgeBase("kb-b", "acme/kb-b"),
		secret("kb-a-webhook", map[string]string{v1alpha1.SecretKeyWebhook: secretA}),
		secret("kb-b-webhook", map[string]string{v1alpha1.SecretKeyWebhook: secretB}),
		newSource("api-a", "acme/api", "kb-a", now), newSource("api-b", "Acme/API", "kb-b", now),
		newSource("web-a", "acme/web", "kb-a", now),
	)
	q := &queueRecorder{}
	return &Webhook{Client: c, Secrets: c, Enqueue: q.add, Tracker: NewTracker(), Logger: testLogger(&syncBuffer{})}, q
}

func deliver(t *testing.T, w *Webhook, event, body, sig string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, req)
	return rr.Code
}

const mergedPR = `{"action":"closed","repository":{"full_name":"acme/api","default_branch":"main"},
"pull_request":{"merged":true,"merged_at":"2026-11-22T10:00:00Z","base":{"ref":"main"}},"sender":{"login":"dev"}}`

func TestWebhookEnqueuesOnlyTheMatchingSource(t *testing.T) {
	w, q := webhookFixture(t)
	if code := deliver(t, w, "pull_request", mergedPR, Sign([]byte(secretA), []byte(mergedPR))); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if got := q.items(); !slices.Equal(got, []string{"team/api-a"}) {
		t.Fatalf("enqueued %v, want only team/api-a", got)
	}
	if at, ok := w.Tracker.proposed("team/api-a"); !ok || !at.Equal(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("merge time = %v, %v", at, ok)
	}

	w, q = webhookFixture(t)
	push := `{"ref":"refs/heads/main","repository":{"full_name":"acme/api","default_branch":"main"},"sender":{"login":"dev"}}`
	if code := deliver(t, w, "push", push, Sign([]byte(secretB), []byte(push))); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if got := q.items(); !slices.Equal(got, []string{"team/api-b"}) {
		t.Fatalf("enqueued %v, want only team/api-b", got)
	}
}

func TestWebhookRejectsBadSignatures(t *testing.T) {
	for name, sig := range map[string]string{
		"missing":      "",
		"wrong secret": Sign([]byte("not-a-configured-secret"), []byte(mergedPR)),
		"malformed":    "sha256=nothex",
		"other body":   Sign([]byte(secretA), []byte(mergedPR+" ")),
	} {
		t.Run(name, func(t *testing.T) {
			w, q := webhookFixture(t)
			before := counter(t, metricEventsDropped, attribute.String(attrReason, DropSignature))
			if code := deliver(t, w, "pull_request", mergedPR, sig); code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
			if got := q.items(); len(got) != 0 {
				t.Fatalf("enqueued %v", got)
			}
			if after := counter(t, metricEventsDropped, attribute.String(attrReason, DropSignature)); after != before+1 {
				t.Fatalf("dropped{signature} = %d, want %d", after, before+1)
			}
		})
	}
}

func TestWebhookDrops(t *testing.T) {
	tests := []struct {
		name, event, body, reason string
		bots                      []string
	}{
		{"knowledge repo", "push", `{"ref":"refs/heads/main","repository":{"full_name":"acme/kb-a","default_branch":"main"},"sender":{"login":"dev"}}`, DropKnowledgeRepo, nil},
		{"own bot", "pull_request", strings.Replace(mergedPR, `"dev"`, `"okf-writer[bot]"`, 1), DropBot, []string{"okf-writer[bot]", "okf-review[bot]"}},
		{"push to another branch", "push", `{"ref":"refs/heads/feature","repository":{"full_name":"acme/api","default_branch":"main"},"sender":{"login":"dev"}}`, DropIgnored, nil},
		{"closed without merge", "pull_request", strings.Replace(mergedPR, `"merged":true`, `"merged":false`, 1), DropIgnored, nil},
		{"merged into another branch", "pull_request", strings.Replace(mergedPR, `"base":{"ref":"main"}`, `"base":{"ref":"release"}`, 1), DropIgnored, nil},
		{"ping", "ping", `{"zen":"hi","repository":{"full_name":"acme/api","default_branch":"main"}}`, DropIgnored, nil},
		{"no source", "push", `{"ref":"refs/heads/main","repository":{"full_name":"acme/other","default_branch":"main"}}`, DropNoSource, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, q := webhookFixture(t)
			w.Tracker.SetBots("team/kb-a", tt.bots)
			before := counter(t, metricEventsDropped, attribute.String(attrReason, tt.reason))
			if code := deliver(t, w, tt.event, tt.body, Sign([]byte(secretA), []byte(tt.body))); code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", code)
			}
			if got := q.items(); len(got) != 0 {
				t.Fatalf("enqueued %v", got)
			}
			if after := counter(t, metricEventsDropped, attribute.String(attrReason, tt.reason)); after != before+1 {
				t.Fatalf("dropped{%s} = %d, want %d", tt.reason, after, before+1)
			}
		})
	}
}

func TestWebhookBodyCap(t *testing.T) {
	w, q := webhookFixture(t)
	body := `{"pad":"` + strings.Repeat("x", MaxWebhookBody) + `"}`
	if code := deliver(t, w, "push", body, Sign([]byte(secretA), []byte(body))); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", code)
	}
	if len(q.items()) != 0 {
		t.Fatal("enqueued an oversized delivery")
	}
	req := httptest.NewRequest(http.MethodGet, "/webhook", bytes.NewReader(nil))
	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", rr.Code)
	}
}

// With a shared secret, the knowledge repo of kb-a is still a source of kb-b:
// the filters drop it for kb-a only.
func TestWebhookFiltersPerKnowledgeBase(t *testing.T) {
	now := time.Now()
	c := newClient(t,
		knowledgeBase("kb-a", "acme/kb-a"), knowledgeBase("kb-b", "acme/kb-b"),
		secret("kb-a-webhook", map[string]string{v1alpha1.SecretKeyWebhook: secretA}),
		secret("kb-b-webhook", map[string]string{v1alpha1.SecretKeyWebhook: secretA}),
		newSource("docs-a", "acme/kb-a", "kb-a", now), newSource("docs-b", "acme/kb-a", "kb-b", now),
	)
	q := &queueRecorder{}
	w := &Webhook{Client: c, Secrets: c, Enqueue: q.add, Tracker: NewTracker(), Logger: testLogger(&syncBuffer{})}
	push := `{"ref":"refs/heads/main","repository":{"full_name":"acme/kb-a","default_branch":"main"},"sender":{"login":"dev"}}`
	if code := deliver(t, w, "push", push, Sign([]byte(secretA), []byte(push))); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if got := q.items(); !slices.Equal(got, []string{"team/docs-b"}) {
		t.Fatalf("enqueued %v, want only team/docs-b", got)
	}

	w.Tracker.SetBots("team/kb-a", []string{"okf-writer[bot]"})
	w.Tracker.SetBots("team/kb-b", []string{"okf-writer[bot]"})
	q.got = nil
	bot := strings.Replace(push, `"dev"`, `"okf-writer[bot]"`, 1)
	if code := deliver(t, w, "push", bot, Sign([]byte(secretA), []byte(bot))); code != http.StatusAccepted || len(q.items()) != 0 {
		t.Fatalf("status %d, enqueued %v: a bot of every matching knowledge base must drop", code, q.items())
	}
}
