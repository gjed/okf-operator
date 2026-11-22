// Package controller runs the okf-operator pipeline as a Kubernetes operator:
// a webhook receiver that only enqueues Sources, and a level-triggered
// reconciler that runs one Source at a time per knowledge repository.
package controller

import (
	"context"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// Run stages, used as the failure reason, metric attribute and span name.
const (
	StageKnowledgeBase = "knowledgebase"
	StageCredentials   = "credentials"
	StageDeclined      = "declined"
	StageCheckout      = "checkout"
	StageRouting       = "routing"
	StagePlan          = "plan"
	StageReconcile     = "reconcile"
	StageWrite         = "write"
	StagePropose       = "propose"
	StageGate          = "gate"
	StageReview        = "review"
	StageApprove       = "approve"
	StageDismiss       = "dismiss"
)

// Job is one run of a Source.
type Job struct {
	Source        *v1alpha1.Source
	KnowledgeBase *v1alpha1.KnowledgeBase
	Mode          string // v1alpha1.ModePlan or v1alpha1.ModeReconcile
}

// Outcome is what a run did. A failed run may still have moved the cursor or
// opened a PR before the failing stage.
type Outcome struct {
	// Cursor is the commit the cursor holds after propose, "" before it.
	Cursor string
	// AtHead is true when propose left the cursor at the source HEAD it planned to.
	AtHead bool
	// PR is the bot PR propose opened or updated, or found open.
	PR *v1alpha1.PullRequest
	// Proposed is true when propose pushed a commit to the bot PR.
	Proposed bool
	// Approved is true when the review App approved the PR.
	Approved bool
	// Dismissed counts approvals dismissed after a failed gate.
	Dismissed int
	// BotLogins are the bot users of the knowledge base's Apps.
	BotLogins []string
}

// RunError is a run failure at one stage. Its message is already redacted.
type RunError struct {
	Stage string
	Msg   string
}

func (e *RunError) Error() string { return e.Stage + ": " + e.Msg }

// Pipeline runs one Source.
type Pipeline interface {
	Run(ctx context.Context, job Job) (Outcome, error)
}
