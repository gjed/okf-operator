// Package gate evaluates a knowledge PR against deterministic auto-merge rules
// and encodes the verdict as a versioned JSON report.
package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Version is the report JSON contract version.
const Version = 1

// Rule names, in report order.
const (
	RuleProvenance  = "provenance"
	RuleRoutedSet   = "routed_set"
	RuleAllowlist   = "allowlist"
	RuleChangeShape = "change_shape"
	RuleSize        = "size"
	RuleLinks       = "links"
	// RuleReview is the optional LLM review, after every deterministic rule.
	RuleReview = "review"
)

// Report is the JSON document `gate` prints. Field order is the output order.
type Report struct {
	Version int      `json:"version"`
	PR      int      `json:"pr"`
	HeadSHA string   `json:"head_sha"`
	Pass    bool     `json:"pass"`
	Rules   []Result `json:"rules"`
}

// Result is the outcome of one rule. Details is empty when the rule passes.
type Result struct {
	Name    string   `json:"name"`
	Pass    bool     `json:"pass"`
	Details []string `json:"details"`
}

// newResult builds a result that passes exactly when there are no details.
func newResult(name string, details []string) Result {
	return Result{Name: name, Pass: len(details) == 0, Details: details}
}

// Add appends a rule result and recomputes Pass. Every input, including a
// later LLM review, goes through Add or Finalize, so none can turn a failed
// report into a passing one.
func (r *Report) Add(res Result) {
	r.Rules = append(r.Rules, res)
	r.Finalize()
}

// Finalize sorts every rule's details and sets Pass to the conjunction of all
// rules. A report with no rules does not pass.
func (r *Report) Finalize() {
	r.Pass = len(r.Rules) > 0
	for i := range r.Rules {
		res := &r.Rules[i]
		if res.Details == nil {
			res.Details = []string{}
		}
		slices.Sort(res.Details)
		res.Details = slices.Compact(res.Details)
		res.Pass = res.Pass && len(res.Details) == 0
		r.Pass = r.Pass && res.Pass
	}
}

// Decode reads one report v1. Unknown keys and other versions fail. The
// decoded Pass is kept as read; callers that act on it call Finalize first.
func Decode(rd io.Reader) (*Report, error) {
	dec := json.NewDecoder(rd)
	dec.DisallowUnknownFields()
	var r Report
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode gate report: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decode gate report: more than one JSON document")
	}
	if r.Version != Version {
		return nil, fmt.Errorf("gate report version %d is not supported, want %d", r.Version, Version)
	}
	return &r, nil
}

// Encode writes r as indented JSON. The same report always encodes to the same bytes.
func Encode(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}
