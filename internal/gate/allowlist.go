package gate

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/gjed/okf-operator/internal/policy"
	"github.com/gjed/okf-operator/internal/provenance"
)

// allowlist passes when the plan's bundle and the concept type of every
// changed page are on the policy's auto-merge allowlist. The type is read from
// the page at the base commit, so a PR cannot retype a page into the
// allowlist; a head type that differs from the base type fails the rule.
func allowlist(ctx context.Context, r Reader, repo, baseSHA, bundle string, pol *policy.Policy, pages map[string][]byte) (Result, error) {
	var details []string
	if !pol.AllowsBundle(bundle) {
		details = append(details, fmt.Sprintf("bundle %s is not in auto_merge.bundles", bundle))
	}
	for _, page := range slices.Sorted(maps.Keys(pages)) {
		base, found, err := r.File(ctx, repo, baseSHA, page)
		if err != nil {
			return Result{}, err
		}
		if !found {
			details = append(details, fmt.Sprintf("%s: not a page at base commit %s, so it has no allowed type", page, baseSHA))
			continue
		}
		baseType, err := provenance.Type(base)
		if err != nil {
			details = append(details, fmt.Sprintf("%s: base page: %v", page, err))
			continue
		}
		headType, err := provenance.Type(pages[page])
		switch {
		case err != nil:
			details = append(details, fmt.Sprintf("%s: %v", page, err))
		case headType != baseType:
			details = append(details, fmt.Sprintf("%s: type changed from %q to %q", page, baseType, headType))
		case baseType == "":
			details = append(details, fmt.Sprintf("%s: no type", page))
		case !pol.AllowsType(baseType):
			details = append(details, fmt.Sprintf("%s: type %q is not in auto_merge.types", page, baseType))
		}
	}
	return newResult(RuleAllowlist, details), nil
}
