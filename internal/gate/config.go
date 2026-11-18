package gate

import (
	"context"
	"fmt"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/policy"
	"github.com/gjed/okf-operator/internal/routing"
)

// loadPolicy reads policy.yaml and routing.yaml at the base commit, never at
// the head, and checks that routing binds the plan's source to its bundle.
func loadPolicy(ctx context.Context, r Reader, repo, baseSHA string, p *plan.Plan) (*policy.Policy, error) {
	read := func(file string) ([]byte, error) {
		data, found, err := r.File(ctx, repo, baseSHA, file)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%s not found in %s at base commit %s", file, repo, baseSHA)
		}
		return data, nil
	}

	data, err := read(policy.DefaultPath)
	if err != nil {
		return nil, err
	}
	pol, err := policy.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", policy.DefaultPath, err)
	}

	if data, err = read(routing.DefaultPath); err != nil {
		return nil, err
	}
	rm, err := routing.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", routing.DefaultPath, err)
	}
	src := rm.Source(p.Source)
	switch {
	case src == nil:
		return nil, fmt.Errorf("plan source %s is not onboarded in %s", p.Source, routing.DefaultPath)
	case src.Bundle != p.Bundle:
		return nil, fmt.Errorf("plan bundle %q differs from bundle %q that %s binds to %s", p.Bundle, src.Bundle, routing.DefaultPath, p.Source)
	}
	return pol, nil
}
