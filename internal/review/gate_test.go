package review

import (
	"reflect"
	"testing"

	"github.com/gjed/okf-operator/internal/gate"
)

func TestGateInput(t *testing.T) {
	tests := []struct {
		name string
		r    Result
		want gate.Review
	}{
		{"pass", Result{PR: 7, HeadSHA: "h", Pass: true, Pages: []Page{{Path: "a.md", Pass: true}}},
			gate.Review{PR: 7, HeadSHA: "h", Pass: true, Details: []string{}}},
		{"failures", Result{PR: 7, HeadSHA: "h", Pages: []Page{
			{Path: "a.md", Reason: ReasonPatchUnavailable},
			{Path: "b.md", Unsupported: []string{"x", "y"}},
			{Path: "c.md", Pass: true},
		}}, gate.Review{PR: 7, HeadSHA: "h", Details: []string{"a.md: patch_unavailable", "b.md: unsupported: x", "b.md: unsupported: y"}}},
		{"no pages", Result{PR: 7, HeadSHA: "h", Pass: true},
			gate.Review{PR: 7, HeadSHA: "h", Details: []string{"no changed page was reviewed"}}},
		{"page pass with sentences", Result{PR: 7, HeadSHA: "h", Pass: true, Pages: []Page{{Path: "a.md", Pass: true, Unsupported: []string{"x"}}}},
			gate.Review{PR: 7, HeadSHA: "h", Details: []string{"a.md: unsupported: x"}}},
		{"page pass with a reason", Result{PR: 7, HeadSHA: "h", Pass: true, Pages: []Page{{Path: "a.md", Pass: true, Reason: ReasonReplyInvalid}}},
			gate.Review{PR: 7, HeadSHA: "h", Details: []string{"a.md: reply_invalid"}}},
		{"edited top-level pass", Result{PR: 7, HeadSHA: "h", Pass: true, Pages: []Page{{Path: "a.md", Unsupported: []string{"x"}}}},
			gate.Review{PR: 7, HeadSHA: "h", Details: []string{"a.md: unsupported: x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.GateInput(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GateInput = %+v, want %+v", got, tt.want)
			}
		})
	}
}
