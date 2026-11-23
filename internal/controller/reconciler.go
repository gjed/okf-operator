package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// Defaults for unset Source intervals. The CRD applies the same defaults.
const (
	DefaultRequeueInterval   = time.Hour
	DefaultReconcileInterval = 7 * 24 * time.Hour
	// DefaultBusyRequeue is how soon a Source whose knowledge repo is busy is retried.
	DefaultBusyRequeue = 15 * time.Second
)

// SourceReconciler runs Sources. Runs of Sources that share a knowledge repo
// never overlap: each run holds a lock keyed by the knowledge repo, and a
// Source whose knowledge repo is busy is requeued.
type SourceReconciler struct {
	Client      client.Client
	Pipeline    Pipeline
	Recorder    events.EventRecorder
	Tracker     *Tracker
	Logger      *slog.Logger
	Now         func() time.Time
	BusyRequeue time.Duration

	locks keyedLock
}

// keyedLock is a set of non-blocking locks, one per key.
type keyedLock struct {
	mu   sync.Mutex
	held map[string]bool
}

func (l *keyedLock) tryLock(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[string]bool{}
	}
	if l.held[key] {
		return false
	}
	l.held[key] = true
	return true
}

func (l *keyedLock) unlock(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, key)
}

func duration(d *metav1.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return d.Duration
}

// reconcileBase is the time the next reconcile run is counted from.
func reconcileBase(s *v1alpha1.Source) time.Time {
	if s.Status.LastReconcileTime != nil {
		return s.Status.LastReconcileTime.Time
	}
	return s.CreationTimestamp.Time
}

// mode picks reconcile when the reconcile interval has passed, else plan.
func mode(s *v1alpha1.Source, now time.Time) string {
	every := duration(s.Spec.ReconcileInterval, DefaultReconcileInterval)
	if every > 0 && !now.Before(reconcileBase(s).Add(every)) {
		return v1alpha1.ModeReconcile
	}
	return v1alpha1.ModePlan
}

// next is the requeue delay: the requeue interval, or less when the next
// reconcile run is due sooner. A due reconcile that failed is retried at
// the requeue interval.
func next(s *v1alpha1.Source, now time.Time) time.Duration {
	d := duration(s.Spec.RequeueInterval, DefaultRequeueInterval)
	if d <= 0 {
		d = DefaultRequeueInterval
	}
	if every := duration(s.Spec.ReconcileInterval, DefaultReconcileInterval); every > 0 {
		if until := reconcileBase(s).Add(every).Sub(now); until > 0 && until < d {
			d = until
		}
	}
	return d
}

// Reconcile runs one Source.
func (r *SourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	src := &v1alpha1.Source{}
	if err := r.Client.Get(ctx, req.NamespacedName, src); err != nil {
		if apierrors.IsNotFound(err) {
			r.Tracker.forget(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	now := r.Now()
	kb := &v1alpha1.KnowledgeBase{}
	kbKey := types.NamespacedName{Namespace: src.Namespace, Name: src.Spec.KnowledgeBaseRef.Name}
	if err := r.Client.Get(ctx, kbKey, kb); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		runErr := &RunError{Stage: StageKnowledgeBase, Msg: fmt.Sprintf("KnowledgeBase %s not found", kbKey.Name)}
		return ctrl.Result{RequeueAfter: next(src, now)}, r.finish(ctx, src, v1alpha1.ModePlan, now, Outcome{}, runErr)
	}

	lockKey := strings.ToLower(kb.Spec.Repository)
	if !r.locks.tryLock(lockKey) {
		busy := r.BusyRequeue
		if busy <= 0 {
			busy = DefaultBusyRequeue
		}
		r.Logger.DebugContext(ctx, "knowledge repo busy, requeued", "source", req.String(), "knowledge_repo", kb.Spec.Repository)
		runQueued(ctx, "busy")
		return ctrl.Result{RequeueAfter: busy}, nil
	}
	defer r.locks.unlock(lockKey)

	m := mode(src, now)
	attrs := []attribute.KeyValue{
		attribute.String(attrSource, src.Spec.Repository),
		attribute.String(attrKB, kb.Spec.Repository),
		attribute.String(attrMode, m),
	}
	ctx, span := otel.Tracer("okf-operator").Start(ctx, "controller run", trace.WithNewRoot())
	span.SetAttributes(attrs...)
	span.SetAttributes(attribute.String("k8s.namespace.name", src.Namespace), attribute.String("okf.controller.source", src.Name))
	start := time.Now()
	out, err := r.Pipeline.Run(ctx, Job{Source: src.DeepCopy(), KnowledgeBase: kb.DeepCopy(), Mode: m})
	elapsed := time.Since(start)
	r.Tracker.SetBots(kbKey.String(), out.BotLogins)

	outcome := "success"
	var runErr *RunError
	if err != nil {
		outcome = "failure"
		if !errors.As(err, &runErr) {
			runErr = &RunError{Stage: "run", Msg: err.Error()}
		}
		span.SetAttributes(attribute.String(attrStage, runErr.Stage))
		span.SetStatus(codes.Error, runErr.Error())
		count(ctx, metricRunFailures, "Source runs that failed, by stage", "{run}",
			attribute.String(attrStage, runErr.Stage), attribute.String(attrMode, m))
	}
	if out.PR != nil {
		span.SetAttributes(attribute.Int("okf.controller.pr", out.PR.Number))
	}
	span.SetAttributes(attribute.String(attrOutcome, outcome), attribute.Bool("okf.controller.approved", out.Approved))
	span.End()
	seconds(ctx, metricRunDuration, "Duration of one Source run", elapsed, attribute.String(attrMode, m), attribute.String(attrOutcome, outcome))
	if out.Approved {
		count(ctx, metricPRsApproved, "Knowledge PRs approved by the review App", "{pr}")
	}
	if out.Proposed {
		if at, ok := r.Tracker.proposed(req.String()); ok {
			seconds(ctx, metricMergeToPR, "Time from a source PR merge to the knowledge PR that carries it", r.Now().Sub(at))
		}
	} else if out.AtHead {
		r.Tracker.proposed(req.String()) // nothing to propose: the merge needed no page
	}
	if out.AtHead {
		r.Tracker.atHead(req.String(), src.Spec.Repository, r.Now())
	}

	var finishErr error
	if runErr != nil {
		finishErr = runErr
	}
	if err := r.finish(ctx, src, m, now, out, finishErr); err != nil {
		return ctrl.Result{}, err
	}
	// Re-read the schedule from the updated status.
	return ctrl.Result{RequeueAfter: next(src, r.Now())}, nil
}

// finish writes the run's status and event. src is updated in place.
func (r *SourceReconciler) finish(ctx context.Context, src *v1alpha1.Source, m string, started time.Time, out Outcome, runErr error) error {
	end := metav1.NewTime(r.Now())
	var re *RunError
	failed := errors.As(runErr, &re)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := &v1alpha1.Source{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(src), cur); err != nil {
			return err
		}
		st := &cur.Status
		st.ObservedGeneration = cur.Generation
		st.LastRunTime = &end
		st.LastRunMode = m
		if out.Cursor != "" {
			st.LastCursor = out.Cursor
		}
		if out.PR != nil {
			pr := *out.PR
			st.LastPR = &pr
		}
		cond := metav1.Condition{Type: v1alpha1.ConditionReady, ObservedGeneration: cur.Generation}
		if failed {
			st.LastError = re.Error()
			cond.Status = metav1.ConditionFalse
			cond.Reason = reason(re.Stage)
			cond.Message = re.Error()
		} else {
			st.LastError = ""
			cond.Status = metav1.ConditionTrue
			cond.Reason = "RunSucceeded"
			cond.Message = fmt.Sprintf("%s run succeeded", m)
			if m == v1alpha1.ModeReconcile {
				t := metav1.NewTime(started)
				st.LastReconcileTime = &t
			}
		}
		apimeta.SetStatusCondition(&st.Conditions, cond)
		if err := r.Client.Status().Update(ctx, cur); err != nil {
			return err
		}
		*src = *cur
		return nil
	})
	if err != nil {
		return err
	}
	if r.Recorder != nil {
		if failed {
			r.Recorder.Eventf(src, nil, corev1.EventTypeWarning, "RunFailed", "Run", "%s run failed at %s", m, re.Error())
		} else {
			r.Recorder.Eventf(src, nil, corev1.EventTypeNormal, "RunSucceeded", "Run", "%s run succeeded%s", m, summary(out))
		}
	}
	if failed {
		r.Logger.ErrorContext(ctx, "run failed", "source", src.Namespace+"/"+src.Name, "mode", m, "stage", re.Stage, "err", re.Msg)
	} else {
		r.Logger.InfoContext(ctx, "run succeeded", "source", src.Namespace+"/"+src.Name, "mode", m, "cursor", out.Cursor, "approved", out.Approved)
	}
	return nil
}

func summary(out Outcome) string {
	var b strings.Builder
	if out.PR != nil {
		fmt.Fprintf(&b, ", PR #%d", out.PR.Number)
	}
	if out.Cursor != "" {
		fmt.Fprintf(&b, ", cursor %s", out.Cursor)
	}
	if out.Approved {
		b.WriteString(", approved")
	}
	if out.Dismissed > 0 {
		fmt.Fprintf(&b, ", %d approval(s) dismissed", out.Dismissed)
	}
	return b.String()
}

// reason turns a stage into a condition reason: write -> WriteFailed.
func reason(stage string) string {
	if stage == "" {
		return "RunFailed"
	}
	if stage == StageKnowledgeBase {
		return "KnowledgeBaseNotFound"
	}
	return strings.ToUpper(stage[:1]) + stage[1:] + "Failed"
}
