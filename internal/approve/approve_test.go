package approve

import (
	"context"
	"errors"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/gate"
)

const head = "2222222222222222222222222222222222222222"

type fakePRs struct {
	head     string
	headErr  error
	approved []string
}

func (f *fakePRs) Head(context.Context, string, int) (string, error) { return f.head, f.headErr }

func (f *fakePRs) Approve(_ context.Context, _ string, _ int, sha, _ string) error {
	f.approved = append(f.approved, sha)
	return nil
}

func report(pass bool, rules ...gate.Result) *gate.Report {
	return &gate.Report{Version: gate.Version, PR: 7, HeadSHA: head, Pass: pass, Rules: rules}
}

func rule(name string, pass bool, details ...string) gate.Result {
	return gate.Result{Name: name, Pass: pass, Details: details}
}

func TestRun(t *testing.T) {
	links, review := rule(gate.RuleLinks, true), rule(gate.RuleReview, true)
	tests := []struct {
		name     string
		report   *gate.Report
		head     string
		approved bool
		reason   string
	}{
		{"approval", report(true, links, review), head, true, ""},
		{"gate failed", report(false, rule(gate.RuleLinks, false, "x"), review), head, false, ReasonGateFailed},
		{"edited pass", report(true, rule(gate.RuleLinks, true, "broken link"), review), head, false, ReasonGateFailed},
		{"review missing", report(true, links), head, false, ReasonReviewMissing},
		{"head moved", report(true, links, review), "3333333333333333333333333333333333333333", false, ReasonHeadMoved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prs := &fakePRs{head: tt.head}
			out, err := Run(context.Background(), prs, "acme/kb", 7, tt.report)
			if err != nil {
				t.Fatal(err)
			}
			want := Outcome{Version: 1, PR: 7, HeadSHA: head, Approved: tt.approved, Reason: tt.reason}
			if *out != want {
				t.Fatalf("outcome = %+v, want %+v", *out, want)
			}
			if tt.approved != (len(prs.approved) == 1 && prs.approved[0] == head) {
				t.Errorf("approvals posted = %v", prs.approved)
			}
			if !tt.approved && len(prs.approved) != 0 {
				t.Errorf("posted an approval: %v", prs.approved)
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	ok := report(true, rule(gate.RuleReview, true))
	if _, err := Run(context.Background(), &fakePRs{head: head}, "acme/kb", 8, ok); err == nil {
		t.Error("report for another PR: no error")
	}
	prs := &fakePRs{headErr: errors.New("boom")}
	if _, err := Run(context.Background(), prs, "acme/kb", 7, ok); err == nil || len(prs.approved) != 0 {
		t.Errorf("head error: err %v, approvals %v", err, prs.approved)
	}
}

func TestSpanAttributes(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	ctx, span := tp.Tracer("test").Start(context.Background(), "approve")
	if _, err := Run(ctx, &fakePRs{head: "moved"}, "acme/kb", 7, report(true, rule(gate.RuleReview, true))); err != nil {
		t.Fatal(err)
	}
	span.End()
	attrs := map[string]string{}
	for _, kv := range spans.Ended()[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	want := map[string]string{"okf.approve.pr": "7", "okf.approve.head_sha": head, "okf.approve.approved": "false", "okf.approve.reason": ReasonHeadMoved}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("%s = %q, want %q", k, attrs[k], v)
		}
	}
}
