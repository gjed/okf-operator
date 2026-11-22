package controller

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// timedPipeline records when each run started.
type timedPipeline struct {
	mu     sync.Mutex
	starts []time.Time
}

func (p *timedPipeline) Run(context.Context, Job) (Outcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, time.Now())
	return Outcome{AtHead: true}, nil
}

func (p *timedPipeline) runs() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.starts...)
}

// TestManagerRequeuesWithoutWebhook runs the manager against a real API
// server: the CRDs install and default, and a Source runs again every
// requeue interval with no webhook, without its own status writes waking it.
func TestManagerRequeuesWithoutWebhook(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run `make test` to fetch the envtest binaries")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "deploy", "crds")}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	scheme, err := Scheme()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &timedPipeline{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Config: cfg, WebhookAddr: "127.0.0.1:0", HealthAddr: "0", Concurrency: 2,
			Pipeline: p, Logger: testLogger(&syncBuffer{})})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager: %v", err)
		}
	})

	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	bad := newSource("bad", "not a repo", "kb", time.Now())
	if err := c.Create(ctx, bad); err == nil {
		t.Fatal("the CRD accepted an invalid repository")
	}
	if err := c.Create(ctx, knowledgeBase("kb", "acme/kb")); err != nil {
		t.Fatal(err)
	}
	src := newSource("api", "acme/api", "kb", time.Time{})
	src.Spec.RequeueInterval = &metav1.Duration{Duration: time.Second}
	if err := c.Create(ctx, src); err != nil {
		t.Fatal(err)
	}
	if src.Spec.ReconcileInterval == nil || src.Spec.ReconcileInterval.Duration != DefaultReconcileInterval {
		t.Fatalf("reconcileInterval default = %v", src.Spec.ReconcileInterval)
	}

	deadline := time.Now().Add(30 * time.Second)
	for len(p.runs()) < 3 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	runs := p.runs()
	if len(runs) < 3 {
		t.Fatalf("%d runs in 30s, want at least 3 from the periodic requeue", len(runs))
	}
	for i := 1; i < len(runs); i++ {
		if gap := runs[i].Sub(runs[i-1]); gap < 900*time.Millisecond {
			t.Fatalf("runs %d and %d only %v apart: a status write woke the Source", i-1, i, gap)
		}
	}
	got := &v1alpha1.Source{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(src), got); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || got.Status.LastRunTime == nil {
		t.Fatalf("status = %+v", got.Status)
	}
}
