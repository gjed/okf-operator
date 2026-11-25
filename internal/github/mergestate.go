package github

import (
	"context"
	"fmt"
	"time"

	gogithub "github.com/google/go-github/v93/github"
)

// MergeStateUnknown is the mergeable_state GitHub reports while it computes
// the state, which it does lazily after a push.
const MergeStateUnknown = "unknown"

// Poll bounds the retries of a read that GitHub answers lazily. The zero
// value is five reads with a delay that starts at 500 ms and doubles, about
// 7.5 s in all.
type Poll struct {
	// Reads is the most reads made; 0 means 5.
	Reads int
	// Delay is the wait before the second read, doubled before each later
	// one; 0 means 500 ms.
	Delay time.Duration
}

func (p Poll) reads() int {
	if p.Reads > 0 {
		return p.Reads
	}
	return 5
}

func (p Poll) delay() time.Duration {
	if p.Delay > 0 {
		return p.Delay
	}
	return 500 * time.Millisecond
}

// Until calls read until done reports true or the reads are used up, waiting
// between reads. It returns whether done was reached, and any read error.
func (p Poll) Until(ctx context.Context, read func(ctx context.Context) (done bool, err error)) (bool, error) {
	delay := p.delay()
	for i := range p.reads() {
		if i > 0 {
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return false, ctx.Err()
			case <-t.C:
			}
			delay *= 2
		}
		done, err := read(ctx)
		if err != nil || done {
			return done, err
		}
	}
	return false, nil
}

// PullMergeState reads pull request n and, while its mergeable_state is
// "unknown", reads it again within p's bounds. It returns the last PR read;
// its state may still be "unknown".
func PullMergeState(ctx context.Context, c *gogithub.Client, owner, name string, n int, p Poll) (*gogithub.PullRequest, error) {
	var pr *gogithub.PullRequest
	_, err := p.Until(ctx, func(ctx context.Context) (bool, error) {
		var err error
		if pr, _, err = c.PullRequests.Get(ctx, owner, name, n); err != nil {
			return false, fmt.Errorf("get %s/%s#%d: %w", owner, name, n, err)
		}
		return pr.GetMergeableState() != MergeStateUnknown, nil
	})
	return pr, err
}
