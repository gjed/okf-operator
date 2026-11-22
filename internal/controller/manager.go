package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/gjed/okf-operator/api/v1alpha1"
)

// Options configure the controller manager.
type Options struct {
	// Config is the cluster config; nil loads it like kubectl.
	Config                  *rest.Config
	WebhookAddr             string
	HealthAddr              string
	LeaderElect             bool
	LeaderElectionNamespace string
	// Namespace limits the watched namespace; "" watches all.
	Namespace   string
	Concurrency int
	// Pipeline runs one Source; nil uses a Runner with APIURL and ToolVersion.
	Pipeline    Pipeline
	APIURL      string
	ToolVersion string
	Logger      *slog.Logger
}

// enqueueBuffer is how many webhook enqueues may wait for the controller.
// Beyond it events are dropped; the periodic requeue covers them.
const enqueueBuffer = 1024

// Scheme returns a scheme with the core kinds and okf.dev/v1alpha1.
func Scheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

// Run starts the manager and blocks until ctx is done.
func Run(ctx context.Context, o Options) error {
	ctrl.SetLogger(logr.FromSlogHandler(o.Logger.Handler()))
	cfg := o.Config
	if cfg == nil {
		var err error
		if cfg, err = ctrl.GetConfig(); err != nil {
			return err
		}
	}
	scheme, err := Scheme()
	if err != nil {
		return err
	}
	mo := manager.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: "0"}, // metrics go through OpenTelemetry
		HealthProbeBindAddress:  o.HealthAddr,
		LeaderElection:          o.LeaderElect,
		LeaderElectionID:        "okf-operator.okf.dev",
		LeaderElectionNamespace: o.LeaderElectionNamespace,
	}
	if o.Namespace != "" {
		mo.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{o.Namespace: {}}}
	}
	mgr, err := ctrl.NewManager(cfg, mo)
	if err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return err
	}

	tracker := NewTracker()
	pipeline := o.Pipeline
	if pipeline == nil {
		pipeline = &Runner{
			Secrets: mgr.GetAPIReader(), APIURL: o.APIURL, Checkout: TarballCheckout{},
			ToolVersion: o.ToolVersion, Logger: o.Logger, Now: time.Now,
		}
	}
	rec := &SourceReconciler{
		Client: mgr.GetClient(), Pipeline: pipeline, Recorder: mgr.GetEventRecorder("okf-operator"),
		Tracker: tracker, Logger: o.Logger, Now: time.Now,
	}
	queue := make(chan event.GenericEvent, enqueueBuffer)
	// Status updates do not change the generation, so a run's own status
	// write never triggers another run.
	err = ctrl.NewControllerManagedBy(mgr).
		Named("source").
		For(&v1alpha1.Source{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(source.Channel(queue, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: max(o.Concurrency, 1)}).
		Complete(rec)
	if err != nil {
		return err
	}

	hook := &Webhook{
		Client: mgr.GetClient(), Secrets: mgr.GetAPIReader(), Tracker: tracker, Logger: o.Logger,
		Enqueue: func(nn types.NamespacedName) {
			select {
			case queue <- event.GenericEvent{Object: &v1alpha1.Source{ObjectMeta: metav1.ObjectMeta{Namespace: nn.Namespace, Name: nn.Name}}}:
			default:
				o.Logger.Warn("enqueue buffer full, event dropped; the periodic requeue covers it", "source", nn.String())
			}
		},
	}
	// The receiver runs on the leader only, where the controller consumes
	// what it enqueues.
	if err := mgr.Add(webhookServer(o.WebhookAddr, hook)); err != nil {
		return err
	}
	return mgr.Start(ctx)
}

func webhookServer(addr string, h http.Handler) manager.RunnableFunc {
	return func(ctx context.Context) error {
		mux := http.NewServeMux()
		mux.Handle("/webhook", h)
		srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute}
		errc := make(chan error, 1)
		go func() { errc <- srv.ListenAndServe() }()
		select {
		case err := <-errc:
			return err
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(sctx); err != nil {
				return err
			}
			if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		}
	}
}
