package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// fakePipeline records jobs and answers with run.
type fakePipeline struct {
	mu   sync.Mutex
	jobs []Job
	run  func(ctx context.Context, job Job) (Outcome, error)
}

func (f *fakePipeline) Run(ctx context.Context, job Job) (Outcome, error) {
	f.mu.Lock()
	f.jobs = append(f.jobs, job)
	f.mu.Unlock()
	if f.run == nil {
		return Outcome{Cursor: strings.Repeat("c", 40), AtHead: true}, nil
	}
	return f.run(ctx, job)
}

func (f *fakePipeline) modes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, j := range f.jobs {
		out = append(out, j.Mode)
	}
	return out
}

func request(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}}
}

func getSource(t *testing.T, r *SourceReconciler, name string) *v1alpha1.Source {
	t.Helper()
	s := &v1alpha1.Source{}
	if err := r.Client.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSuccessfulRunStatusAndRequeue(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	src := newSource("api", "acme/api", "kb", now.Add(-time.Hour))
	src.Spec.RequeueInterval = &metav1.Duration{Duration: 10 * time.Minute}
	p := &fakePipeline{run: func(context.Context, Job) (Outcome, error) {
		return Outcome{Cursor: strings.Repeat("b", 40), AtHead: true, Proposed: true,
			PR: &v1alpha1.PullRequest{Number: 12, URL: "https://github.com/acme/kb/pull/12"}}, nil
	}}
	r, rec, _ := newReconciler(newClient(t, knowledgeBase("kb", "acme/kb"), src), p, now)
	res, err := r.Reconcile(context.Background(), request("api"))
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 10*time.Minute {
		t.Fatalf("RequeueAfter = %v, want the requeue interval", res.RequeueAfter)
	}
	if got := p.modes(); len(got) != 1 || got[0] != v1alpha1.ModePlan {
		t.Fatalf("modes = %v", got)
	}
	st := getSource(t, r, "api").Status
	cond := apimeta.FindStatusCondition(st.Conditions, v1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || st.LastPR == nil || st.LastPR.Number != 12 ||
		st.LastCursor != strings.Repeat("b", 40) || st.LastError != "" || st.LastRunMode != v1alpha1.ModePlan || st.ObservedGeneration != 1 {
		t.Fatalf("status = %+v", st)
	}
	if ev := <-rec.Events; !strings.Contains(ev, "Normal RunSucceeded") || !strings.Contains(ev, "PR #12") {
		t.Fatalf("event = %q", ev)
	}
}

func TestFailedRunSetsErrorCondition(t *testing.T) {
	now := time.Now()
	p := &fakePipeline{run: func(context.Context, Job) (Outcome, error) {
		return Outcome{}, &RunError{Stage: StageWrite, Msg: "model endpoint returned 500"}
	}}
	src := newSource("api", "acme/api", "kb", now)
	src.Status.LastCursor = strings.Repeat("a", 40)
	r, rec, _ := newReconciler(newClient(t, knowledgeBase("kb", "acme/kb"), src), p, now)
	before := counter(t, metricRunFailures, attribute.String(attrStage, StageWrite))
	res, err := r.Reconcile(context.Background(), request("api"))
	if err != nil || res.RequeueAfter != DefaultRequeueInterval {
		t.Fatalf("res %+v, err %v", res, err)
	}
	st := getSource(t, r, "api").Status
	cond := apimeta.FindStatusCondition(st.Conditions, v1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "WriteFailed" ||
		st.LastError != "write: model endpoint returned 500" || st.LastCursor != strings.Repeat("a", 40) {
		t.Fatalf("status = %+v", st)
	}
	if ev := <-rec.Events; !strings.Contains(ev, "Warning RunFailed") || !strings.Contains(ev, "model endpoint returned 500") {
		t.Fatalf("event = %q", ev)
	}
	if after := counter(t, metricRunFailures, attribute.String(attrStage, StageWrite)); after != before+1 {
		t.Fatalf("failures{write} = %d, want %d", after, before+1)
	}
}

func TestMissingKnowledgeBase(t *testing.T) {
	now := time.Now()
	p := &fakePipeline{}
	r, _, _ := newReconciler(newClient(t, newSource("api", "acme/api", "nope", now)), p, now)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(getSource(t, r, "api").Status.Conditions, v1alpha1.ConditionReady)
	if cond == nil || cond.Reason != "KnowledgeBaseNotFound" || len(p.modes()) != 0 {
		t.Fatalf("condition = %+v, runs %v", cond, p.modes())
	}
}

func TestWeeklyReconcileRunsReconcileNotPlan(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	p := &fakePipeline{}
	r, _, _ := newReconciler(newClient(t, knowledgeBase("kb", "acme/kb"), newSource("api", "acme/api", "kb", now.Add(-8*24*time.Hour))), p, now)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	st := getSource(t, r, "api").Status
	if st.LastReconcileTime == nil || !st.LastReconcileTime.Time.Equal(now) || st.LastRunMode != v1alpha1.ModeReconcile {
		t.Fatalf("status = %+v", st)
	}
	res, err := r.Reconcile(context.Background(), request("api"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.modes(); len(got) != 2 || got[0] != v1alpha1.ModeReconcile || got[1] != v1alpha1.ModePlan {
		t.Fatalf("modes = %v, want reconcile then plan", got)
	}
	if res.RequeueAfter != DefaultRequeueInterval {
		t.Fatalf("RequeueAfter = %v", res.RequeueAfter)
	}

	// The requeue shortens to the next reconcile when it is due sooner, and
	// a failed reconcile is retried in reconcile mode.
	r.Now = func() time.Time { return now.Add(7*24*time.Hour - 5*time.Minute) }
	if res, _ = r.Reconcile(context.Background(), request("api")); res.RequeueAfter != 5*time.Minute {
		t.Fatalf("RequeueAfter = %v, want 5m", res.RequeueAfter)
	}
	p.run = func(context.Context, Job) (Outcome, error) {
		return Outcome{}, &RunError{Stage: StageReconcile, Msg: "x"}
	}
	r.Now = func() time.Time { return now.Add(7 * 24 * time.Hour) }
	for range 2 {
		if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
			t.Fatal(err)
		}
	}
	if got := p.modes(); got[len(got)-1] != v1alpha1.ModeReconcile || got[len(got)-2] != v1alpha1.ModeReconcile {
		t.Fatalf("modes = %v, want a retried reconcile", got)
	}
}

func TestReconcileIntervalZeroDisables(t *testing.T) {
	now := time.Now()
	src := newSource("api", "acme/api", "kb", now.Add(-30*24*time.Hour))
	src.Spec.ReconcileInterval = &metav1.Duration{}
	p := &fakePipeline{}
	r, _, _ := newReconciler(newClient(t, knowledgeBase("kb", "acme/kb"), src), p, now)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if got := p.modes(); len(got) != 1 || got[0] != v1alpha1.ModePlan {
		t.Fatalf("modes = %v", got)
	}
}

// Two Sources of one knowledge repo, reconciled at once, never run at the
// same time; a Source of another knowledge repo is not held up.
func TestRunsSerialisedPerKnowledgeRepo(t *testing.T) {
	now := time.Now()
	release := make(chan struct{})
	started := make(chan string, 3)
	var mu sync.Mutex
	running := map[string]int{}
	maxRunning := map[string]int{}
	p := &fakePipeline{run: func(_ context.Context, job Job) (Outcome, error) {
		repo := strings.ToLower(job.KnowledgeBase.Spec.Repository)
		mu.Lock()
		running[repo]++
		maxRunning[repo] = max(maxRunning[repo], running[repo])
		mu.Unlock()
		started <- job.Source.Name
		if job.Source.Name == "api" {
			<-release
		}
		mu.Lock()
		running[repo]--
		mu.Unlock()
		return Outcome{}, nil
	}}
	c := newClient(t,
		knowledgeBase("kb", "acme/kb"), knowledgeBase("kb-alias", "ACME/KB"), knowledgeBase("other", "acme/other"),
		newSource("api", "acme/api", "kb", now), newSource("web", "acme/web", "kb-alias", now), newSource("lib", "acme/lib", "other", now))
	r, _, _ := newReconciler(c, p, now)
	r.BusyRequeue = time.Second

	done := make(chan ctrl.Result, 1)
	go func() {
		res, err := r.Reconcile(context.Background(), request("api"))
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	if got := <-started; got != "api" {
		t.Fatalf("first run = %s", got)
	}
	res, err := r.Reconcile(context.Background(), request("web"))
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != time.Second {
		t.Fatalf("busy Source RequeueAfter = %v, want the busy requeue", res.RequeueAfter)
	}
	if _, err := r.Reconcile(context.Background(), request("lib")); err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "lib" {
		t.Fatalf("second run = %s, want lib while api runs", got)
	}
	close(release)
	<-done
	if _, err := r.Reconcile(context.Background(), request("web")); err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "web" {
		t.Fatalf("third run = %s", got)
	}
	if maxRunning["acme/kb"] != 1 {
		t.Fatalf("max concurrent runs on acme/kb = %d, want 1", maxRunning["acme/kb"])
	}
}
