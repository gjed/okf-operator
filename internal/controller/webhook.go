package controller

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// MaxWebhookBody is the largest webhook body the receiver reads.
const MaxWebhookBody = 5 << 20

// Drop reasons of okf.controller.events.dropped.
const (
	DropSignature     = "signature"
	DropTooLarge      = "too_large"
	DropMalformed     = "malformed"
	DropKnowledgeRepo = "knowledge_repo"
	DropBot           = "bot"
	DropIgnored       = "ignored_event"
	DropNoSource      = "no_source"
)

// Webhook receives GitHub webhooks. It only enqueues Sources; it never runs
// the pipeline.
type Webhook struct {
	// Client lists KnowledgeBases and Sources.
	Client client.Reader
	// Secrets reads the webhook Secrets, uncached.
	Secrets client.Reader
	// Enqueue queues one Source.
	Enqueue func(types.NamespacedName)
	Tracker *Tracker
	Logger  *slog.Logger
}

// payload is the part of a push or pull_request payload the receiver reads.
type payload struct {
	Action     string `json:"action"`
	Ref        string `json:"ref"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	PullRequest *struct {
		Merged   bool       `json:"merged"`
		MergedAt *time.Time `json:"merged_at"`
		Base     struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// enqueues reports whether e of type name may change a Source's HEAD: a push
// to the default branch, or a PR merged into it.
func (e *payload) enqueues(name string) bool {
	def := e.Repository.DefaultBranch
	switch name {
	case "push":
		return def != "" && e.Ref == "refs/heads/"+def
	case "pull_request":
		return e.Action == "closed" && e.PullRequest != nil && e.PullRequest.Merged && e.PullRequest.Base.Ref == def
	}
	return false
}

func (w *Webhook) drop(ctx context.Context, rw http.ResponseWriter, status int, reason string) {
	eventDropped(ctx, reason)
	w.Logger.DebugContext(ctx, "webhook dropped", "reason", reason)
	rw.WriteHeader(status)
}

// ServeHTTP implements http.Handler.
func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		rw.Header().Set("Allow", http.MethodPost)
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := r.Header.Get("X-GitHub-Event")
	eventReceived(ctx, name)
	body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, MaxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.drop(ctx, rw, http.StatusRequestEntityTooLarge, DropTooLarge)
			return
		}
		w.drop(ctx, rw, http.StatusBadRequest, DropMalformed)
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		w.drop(ctx, rw, http.StatusUnauthorized, DropSignature)
		return
	}
	kbs, err := w.verified(ctx, body, sig)
	if err != nil {
		w.Logger.ErrorContext(ctx, "webhook: list knowledge bases", "err", err)
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if len(kbs) == 0 {
		w.drop(ctx, rw, http.StatusUnauthorized, DropSignature)
		return
	}

	var e payload
	if err := json.Unmarshal(body, &e); err != nil {
		w.drop(ctx, rw, http.StatusBadRequest, DropMalformed)
		return
	}
	repo := e.Repository.FullName
	// The filters apply per knowledge base: one knowledge base's knowledge
	// repo may be a source of another that shares the webhook secret.
	var keep []v1alpha1.KnowledgeBase
	reason := ""
	for _, kb := range kbs {
		key := kb.Namespace + "/" + kb.Name
		switch {
		case strings.EqualFold(repo, kb.Spec.Repository):
			reason = cmp.Or(reason, DropKnowledgeRepo)
		case e.Sender.Login != "" && w.Tracker.IsBot(key, e.Sender.Login):
			reason = cmp.Or(reason, DropBot)
		default:
			keep = append(keep, kb)
		}
	}
	if len(keep) == 0 {
		w.drop(ctx, rw, http.StatusAccepted, reason)
		return
	}
	kbs = keep
	if !e.enqueues(name) {
		w.drop(ctx, rw, http.StatusAccepted, DropIgnored)
		return
	}

	var sources v1alpha1.SourceList
	if err := w.Client.List(ctx, &sources); err != nil {
		w.Logger.ErrorContext(ctx, "webhook: list sources", "err", err)
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	n := 0
	for i := range sources.Items {
		s := &sources.Items[i]
		if !strings.EqualFold(s.Spec.Repository, repo) || !matches(kbs, s) {
			continue
		}
		nn := types.NamespacedName{Namespace: s.Namespace, Name: s.Name}
		if e.PullRequest != nil && e.PullRequest.MergedAt != nil {
			w.Tracker.Merged(nn.String(), *e.PullRequest.MergedAt)
		}
		w.Enqueue(nn)
		runQueued(ctx, "webhook")
		n++
	}
	if n == 0 {
		w.drop(ctx, rw, http.StatusAccepted, DropNoSource)
		return
	}
	w.Logger.InfoContext(ctx, "webhook enqueued sources", "event", name, "repo", repo, "sources", n)
	rw.WriteHeader(http.StatusAccepted)
}

// verified returns the KnowledgeBases whose webhook secret signed body. A
// KnowledgeBase whose Secret cannot be read is skipped.
func (w *Webhook) verified(ctx context.Context, body []byte, sig string) ([]v1alpha1.KnowledgeBase, error) {
	var list v1alpha1.KnowledgeBaseList
	if err := w.Client.List(ctx, &list); err != nil {
		return nil, err
	}
	var out []v1alpha1.KnowledgeBase
	for _, kb := range list.Items {
		var s corev1.Secret
		key := types.NamespacedName{Namespace: kb.Namespace, Name: kb.Spec.WebhookSecretRef.Name}
		if err := w.Secrets.Get(ctx, key, &s); err != nil {
			w.Logger.WarnContext(ctx, "webhook: read webhook secret", "knowledge_base", kb.Namespace+"/"+kb.Name, "secret", key.Name, "err", err)
			continue
		}
		if VerifySignature(s.Data[v1alpha1.SecretKeyWebhook], body, sig) {
			out = append(out, kb)
		}
	}
	return out, nil
}

func matches(kbs []v1alpha1.KnowledgeBase, s *v1alpha1.Source) bool {
	for _, kb := range kbs {
		if kb.Namespace == s.Namespace && kb.Name == s.Spec.KnowledgeBaseRef.Name {
			return true
		}
	}
	return false
}
