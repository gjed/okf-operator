package approve

import (
	"context"
	"errors"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/gate"
)

const (
	head = "2222222222222222222222222222222222222222"
	base = "1111111111111111111111111111111111111111"
)

type fakePRs struct {
	head, base string
	headErr    error
	approved   []string
}

func (f *fakePRs) State(context.Context, string, int) (string, string, error) {
	b := f.base
	if b == "" {
		b = base
	}
	return f.head, b, f.headErr
}

func (f *fakePRs) Approve(_ context.Context, _ string, _ int, sha, _ string) error {
	f.approved = append(f.approved, sha)
	return nil
}

func report(pass bool, rules ...gate.Result) *gate.Report {
	return &gate.Report{Version: gate.Version, KnowledgeRepo: "acme/kb", PR: 7, HeadSHA: head, BaseSHA: base, PlanDigest: "sha256:x", Pass: pass, Rules: rules}
}

func rule(name string, pass bool, details ...string) gate.Result {
	return gate.Result{Name: name, Pass: pass, Details: details}
}

// rules is every deterministic rule passing, with links replaced by extra.
func rules(extra ...gate.Result) []gate.Result {
	var out []gate.Result
	for _, name := range deterministicRules[:len(deterministicRules)-1] {
		out = append(out, rule(name, true))
	}
	if len(extra) == 0 {
		extra = []gate.Result{rule(gate.RuleLinks, true)}
	}
	return append(out, extra...)
}

func TestRun(t *testing.T) {
	links, review := rule(gate.RuleLinks, true), rule(gate.RuleReview, true)
	tests := []struct {
		name     string
		report   *gate.Report
		head     string
		base     string
		approved bool
		reason   string
	}{
		{"approval", report(true, rules(links, review)...), head, "", true, ""},
		{"gate failed", report(false, rules(rule(gate.RuleLinks, false, "x"), review)...), head, "", false, ReasonGateFailed},
		{"edited pass", report(true, rules(rule(gate.RuleLinks, true, "broken link"), review)...), head, "", false, ReasonGateFailed},
		{"deterministic rule removed", report(true, links, review), head, "", false, ReasonReportIncomplete},
		{"review missing", report(true, rules()...), head, "", false, ReasonReviewMissing},
		{"rule repeated", report(true, append(rules(links, review), review)...), head, "", false, ReasonReportIncomplete},
		{"head moved", report(true, rules(links, review)...), "3333333333333333333333333333333333333333", "", false, ReasonHeadMoved},
		{"base moved", report(true, rules(links, review)...), head, "4444444444444444444444444444444444444444", false, ReasonBaseMoved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prs := &fakePRs{head: tt.head, base: tt.base}
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
	ok := report(true, rules(rule(gate.RuleLinks, true), rule(gate.RuleReview, true))...)
	if _, err := Run(context.Background(), &fakePRs{head: head}, "acme/kb", 8, ok); err == nil {
		t.Error("report for another PR: no error")
	}
	if _, err := Run(context.Background(), &fakePRs{head: head}, "acme/other", 7, ok); err == nil {
		t.Error("report for another repo: no error")
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
	if _, err := Run(ctx, &fakePRs{head: "moved"}, "acme/kb", 7, report(true, rules(rule(gate.RuleLinks, true), rule(gate.RuleReview, true))...)); err != nil {
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
