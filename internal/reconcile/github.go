package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/gjed/okf-operator/internal/github"
)

// GitHubTree implements TreeLister with the recursive git trees API.
//
// SIMPLIFIED: one recursive call, which GitHub truncates past 100,000 entries
// or 7 MB; a truncated tree fails closed. Upgrade path: walk subtrees one
// call at a time, or a clone-based lister behind the same interface.
type GitHubTree struct {
	Clients github.Clients
}

// Paths returns every blob path of repo at ref, symlinks included.
func (g GitHubTree) Paths(ctx context.Context, repo, ref string) ([]string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	t, _, err := c.Git.GetTree(ctx, owner, name, ref, true)
	if err != nil {
		return nil, fmt.Errorf("list %s tree at %s: %w", repo, ref, err)
	}
	if t.GetTruncated() {
		return nil, fmt.Errorf("%s tree at %s: %w", repo, ref, ErrTruncated)
	}
	paths := make([]string, 0, len(t.Entries))
	for _, e := range t.Entries {
		if e.GetType() == "blob" {
			paths = append(paths, e.GetPath())
		}
	}
	return paths, nil
}
