// Package approve posts the approving review of a knowledge PR with the
// review-only GitHub App, and only for a passing, reviewed gate report whose
// head is still the PR's head.
package approve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	gogithub "github.com/google/go-github/v93/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/okf-operator/internal/gate"
	"github.com/gjed/okf-operator/internal/github"
)

// Version is the outcome JSON contract version.
const Version = 1

// Reasons for not approving.
const (
	ReasonGateFailed       = "gate_failed"
	ReasonReportIncomplete = "report_incomplete"
	ReasonReviewMissing    = "review_missing"
	ReasonHeadMoved        = "head_moved"
	ReasonBaseMoved        = "base_moved"
)

// deterministicRules are the rules every gate report carries. A report
// without one of them was cut or edited, and is never approved.
var deterministicRules = []string{
	gate.RuleProvenance, gate.RuleRoutedSet, gate.RuleAllowlist, gate.RuleChangeShape, gate.RuleSize, gate.RuleLinks,
}

// Outcome is the JSON document `approve` prints. Field order is the output order.
type Outcome struct {
	Version  int    `json:"version"`
	PR       int    `json:"pr"`
	HeadSHA  string `json:"head_sha"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
}

// PullRequests reads a PR's head and base tip and posts an approving review.
type PullRequests interface {
	// State returns the PR's head SHA and its base branch tip.
	State(ctx context.Context, repo string, n int) (head, base string, err error)
	Approve(ctx context.Context, repo string, n int, sha, body string) error
}

// Run approves PR n of repo when report passes with every deterministic rule
// and a passing review rule, and the PR head and base branch tip are still the
// ones the report evaluated. An error means nothing was decided.
func Run(ctx context.Context, prs PullRequests, repo string, n int, report *gate.Report) (*Outcome, error) {
	if report.KnowledgeRepo != repo || report.PR != n {
		return nil, fmt.Errorf("gate report is for %s#%d, not %s#%d", report.KnowledgeRepo, report.PR, repo, n)
	}
	out := &Outcome{Version: Version, PR: n, HeadSHA: report.HeadSHA}
	out.Reason = check(report)
	if out.Reason == "" {
		head, base, err := prs.State(ctx, repo, n)
		if err != nil {
			return nil, err
		}
		switch {
		case head != report.HeadSHA:
			out.Reason = ReasonHeadMoved
		case base != report.BaseSHA:
			out.Reason = ReasonBaseMoved
		}
	}
	if out.Reason == "" {
		body := "okf-operator: every gate rule, including the LLM review, passed at " + report.HeadSHA + "."
		if err := prs.Approve(ctx, repo, n, report.HeadSHA, body); err != nil {
			return nil, err
		}
		out.Approved = true
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.Int("okf.approve.pr", out.PR),
		attribute.String("okf.approve.head_sha", out.HeadSHA),
		attribute.Bool("okf.approve.approved", out.Approved),
		attribute.String("okf.approve.reason", out.Reason),
	)
	return out, nil
}

// check recomputes pass on a copy of the report, so an edited top-level pass
// counts for nothing, and requires every deterministic rule and a passing
// review rule to be present, each once.
func check(report *gate.Report) string {
	r := gate.Report{Rules: make([]gate.Result, len(report.Rules))}
	for i, res := range report.Rules {
		res.Details = slices.Clone(res.Details)
		r.Rules[i] = res
	}
	r.Finalize()
	if !report.Pass || !r.Pass {
		return ReasonGateFailed
	}
	names := make(map[string]bool, len(r.Rules))
	for _, res := range r.Rules {
		if names[res.Name] {
			return ReasonReportIncomplete // gate never repeats a rule
		}
		names[res.Name] = true
	}
	for _, name := range deterministicRules {
		if !names[name] {
			return ReasonReportIncomplete
		}
	}
	if !names[gate.RuleReview] {
		return ReasonReviewMissing
	}
	return ""
}

// Encode writes o as indented JSON.
func Encode(w io.Writer, o *Outcome) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}

// GitHub implements PullRequests with the GitHub REST API.
type GitHub struct {
	Clients github.Clients
}

// State returns the PR's current head SHA and its base branch tip, resolved
// the way gate resolves it.
func (g GitHub) State(ctx context.Context, repo string, n int) (string, string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return "", "", err
	}
	pr, _, err := c.PullRequests.Get(ctx, owner, name, n)
	if err != nil {
		return "", "", fmt.Errorf("get %s#%d: %w", repo, n, err)
	}
	ref := pr.GetBase().GetRef()
	base, _, err := c.Repositories.GetCommitSHA1(ctx, owner, name, ref, "")
	if err != nil {
		return "", "", fmt.Errorf("resolve %s@%s: %w", repo, ref, err)
	}
	return pr.GetHead().GetSHA(), base, nil
}

// Approve posts an APPROVE review on commit sha.
func (g GitHub) Approve(ctx context.Context, repo string, n int, sha, body string) error {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return err
	}
	_, _, err = c.PullRequests.CreateReview(ctx, owner, name, n, &gogithub.PullRequestReviewRequest{
		CommitID: &sha,
		Body:     &body,
		Event:    new("APPROVE"),
	})
	if err != nil {
		return fmt.Errorf("approve %s#%d: %w", repo, n, err)
	}
	return nil
}
