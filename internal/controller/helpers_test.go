package controller

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

var (
	spans   = tracetest.NewSpanRecorder()
	metrics = sdkmetric.NewManualReader()
)

func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(metrics)))
	os.Exit(m.Run())
}

const ns = "team"

// counter sums the data points of an Int64 counter whose attributes include want.
func counter(t *testing.T, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				match := true
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						match = false
					}
				}
				if match {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func secret(name string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func knowledgeBase(name, repo string) *v1alpha1.KnowledgeBase {
	return &v1alpha1.KnowledgeBase{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: v1alpha1.KnowledgeBaseSpec{
			Repository: repo,
			Writer: v1alpha1.Identity{
				AppSecretRef: v1alpha1.SecretRef{Name: name + "-writer"},
				Model:        v1alpha1.Model{BaseURL: "http://model.invalid/v1", Name: "writer-m", APIKeySecretRef: &v1alpha1.SecretRef{Name: name + "-writer-key"}},
			},
			Reviewer: v1alpha1.Identity{
				AppSecretRef: v1alpha1.SecretRef{Name: name + "-review"},
				Model:        v1alpha1.Model{BaseURL: "http://model.invalid/v1", Name: "review-m"},
			},
			WebhookSecretRef: v1alpha1.SecretRef{Name: name + "-webhook"},
		},
	}
}

func newSource(name, repo, kb string, created time.Time) *v1alpha1.Source {
	return &v1alpha1.Source{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(created), Generation: 1},
		Spec:       v1alpha1.SourceSpec{Repository: repo, KnowledgeBaseRef: v1alpha1.KnowledgeBaseRef{Name: kb}},
	}
}

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme, err := Scheme()
	if err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(&v1alpha1.Source{}).Build()
}

// syncBuffer is a log sink safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLogger(w *syncBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newReconciler(c client.Client, p Pipeline, now time.Time) (*SourceReconciler, *events.FakeRecorder, *syncBuffer) {
	rec := events.NewFakeRecorder(100)
	logs := &syncBuffer{}
	return &SourceReconciler{
		Client: c, Pipeline: p, Recorder: rec, Tracker: NewTracker(), Logger: testLogger(logs),
		Now: func() time.Time { return now },
	}, rec, logs
}
