package review

import "github.com/gjed/okf-operator/internal/gate"

// GateInput is the review as `gate` takes it: one detail per page the model
// did not review and one per unsupported sentence. It passes only when the
// result and every page pass, so an edited top-level pass counts for nothing.
func (r *Result) GateInput() gate.Review {
	details := []string{}
	pass := r.Pass && len(r.Pages) > 0
	for _, pg := range r.Pages {
		pass = pass && pg.Pass
		if pg.Pass {
			continue
		}
		if pg.Reason != "" {
			details = append(details, pg.Path+": "+pg.Reason)
		}
		for _, s := range pg.Unsupported {
			details = append(details, pg.Path+": unsupported: "+s)
		}
	}
	if len(r.Pages) == 0 {
		details = append(details, "no changed page was reviewed")
	}
	return gate.Review{PR: r.PR, HeadSHA: r.HeadSHA, Pass: pass, Details: details}
}
