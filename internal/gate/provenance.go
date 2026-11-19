package gate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/provenance"
)

// provenanceRule passes when every changed page carries all five provenance
// fields, names the plan's source as source_repo, has the plan's to as
// source_sha, and that commit exists in the source repo. An unknown commit
// fails the rule; a failed lookup fails the run.
func provenanceRule(ctx context.Context, c CommitChecker, source, to string, pages map[string][]byte) (Result, error) {
	var details []string
	exists := map[string]bool{}
	for _, page := range slices.Sorted(maps.Keys(pages)) {
		f, _, err := provenance.Parse(pages[page])
		if err != nil {
			details = append(details, fmt.Sprintf("%s: %v", page, err))
			continue
		}
		missing := missingFields(f)
		for _, k := range missing {
			details = append(details, fmt.Sprintf("%s: missing %s", page, k))
		}
		if f.SourceRepo != "" && !strings.EqualFold(f.SourceRepo, source) {
			details = append(details, fmt.Sprintf("%s: source_repo %s is not the plan's source %s", page, f.SourceRepo, source))
			continue
		}
		if f.SourceSHA == "" {
			continue
		}
		if !cursor.ValidSHA(f.SourceSHA) {
			details = append(details, fmt.Sprintf("%s: source_sha %q is not a full 40-character commit SHA", page, f.SourceSHA))
			continue
		}
		if f.SourceSHA != to {
			details = append(details, fmt.Sprintf("%s: source_sha %s is not the plan's to %s", page, f.SourceSHA, to))
		}
		ok, seen := exists[f.SourceSHA]
		if !seen {
			if ok, err = c.CommitExists(ctx, source, f.SourceSHA); err != nil {
				return Result{}, err
			}
			exists[f.SourceSHA] = ok
		}
		if !ok {
			details = append(details, fmt.Sprintf("%s: source_sha %s not found in %s", page, f.SourceSHA, source))
		}
	}
	return newResult(RuleProvenance, details), nil
}

func missingFields(f provenance.Fields) []string {
	var out []string
	for _, c := range []struct {
		key  string
		zero bool
	}{
		{provenance.KeySourceRepo, f.SourceRepo == ""},
		{provenance.KeySourceSHA, f.SourceSHA == ""},
		{provenance.KeySourcePaths, len(f.SourcePaths) == 0},
		{provenance.KeyUpdatedAt, f.UpdatedAt.IsZero()},
		{provenance.KeyGeneratedBy, f.GeneratedBy == ""},
	} {
		if c.zero {
			out = append(out, c.key)
		}
	}
	return out
}
