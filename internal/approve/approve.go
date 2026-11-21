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
	ReasonGateFailed    = "gate_failed"
	ReasonReviewMissing = "review_missing"
	ReasonHeadMoved     = "head_moved"
)

// Outcome is the JSON document `approve` prints. Field order is the output order.
type Outcome struct {
	Version  int    `json:"version"`
	PR       int    `json:"pr"`
	HeadSHA  string `json:"head_sha"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
}

// PullRequests reads a PR head and posts an approving review.
type PullRequests interface {
	Head(ctx context.Context, repo string, n int) (string, error)
	Approve(ctx context.Context, repo string, n int, sha, body string) error
}

// Run approves PR n of repo when report passes with a passing review rule and
// the PR head is still the report's head. An error means nothing was decided.
func Run(ctx context.Context, prs PullRequests, repo string, n int, report *gate.Report) (*Outcome, error) {
	if report.PR != n {
		return nil, fmt.Errorf("gate report is for PR #%d, not #%d", report.PR, n)
	}
	out := &Outcome{Version: Version, PR: n, HeadSHA: report.HeadSHA}
	out.Reason = check(report)
	if out.Reason == "" {
		head, err := prs.Head(ctx, repo, n)
		if err != nil {
			return nil, err
		}
		if head != report.HeadSHA {
			out.Reason = ReasonHeadMoved
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
// counts for nothing, and requires a passing review rule.
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
	for _, res := range r.Rules {
		if res.Name == gate.RuleReview && res.Pass {
			return ""
		}
	}
	return ReasonReviewMissing
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

// Head returns the PR's current head SHA.
func (g GitHub) Head(ctx context.Context, repo string, n int) (string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return "", err
	}
	pr, _, err := c.PullRequests.Get(ctx, owner, name, n)
	if err != nil {
		return "", fmt.Errorf("get %s#%d: %w", repo, n, err)
	}
	return pr.GetHead().GetSHA(), nil
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
