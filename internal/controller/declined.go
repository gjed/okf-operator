package controller

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/propose"
)

// rangeRE matches the range marker propose writes before each PR body section.
var rangeRE = regexp.MustCompile(`<!-- okf:range ([0-9a-f]{40})?\.\.([0-9a-f]{40}) -->`)

// declinedScan is how many recently updated closed bot PRs a run looks at.
const declinedScan = 30

// DeclinedPath is the kb-state file listing the declined ranges of source.
func DeclinedPath(source string) string { return "declined/" + source }

type declined struct {
	pr       int
	from, to string
}

func (d declined) line() string {
	from := d.from
	if from == "" {
		from = "-"
	}
	return fmt.Sprintf("%d %s %s\n", d.pr, from, d.to)
}

// recordDeclined appends the source ranges of bot PRs of source closed
// without merging to DeclinedPath(source) on kb-state, once per PR. It is
// bookkeeping: it never blocks a proposal and never touches the cursor. It
// returns how many PRs it recorded.
func recordDeclined(ctx context.Context, clients github.Clients, kbRepo, source string) (int, error) {
	owner, name, _ := strings.Cut(kbRepo, "/")
	c, err := clients.ClientFor(ctx, owner)
	if err != nil {
		return 0, err
	}
	prs, _, err := c.PullRequests.List(ctx, owner, name, &gogithub.PullRequestListOptions{
		State: "closed", Head: owner + ":" + propose.Branch(source), Sort: "updated", Direction: "desc",
		ListOptions: gogithub.ListOptions{PerPage: declinedScan},
	})
	if err != nil {
		return 0, fmt.Errorf("list closed bot PRs in %s: %w", kbRepo, err)
	}
	var found []declined
	for _, pr := range prs {
		if pr.MergedAt != nil && !pr.GetMergedAt().IsZero() {
			continue
		}
		for _, m := range rangeRE.FindAllStringSubmatch(pr.GetBody(), -1) {
			found = append(found, declined{pr: pr.GetNumber(), from: m[1], to: m[2]})
		}
	}
	if len(found) == 0 {
		return 0, nil
	}

	file := DeclinedPath(source)
	where := fmt.Sprintf("%s on %s@%s", file, kbRepo, cursor.Branch)
	branch := true
	var blob, content string
	if _, _, err := c.Git.GetRef(ctx, owner, name, "heads/"+cursor.Branch); github.IsNotFound(err) {
		branch = false
	} else if err != nil {
		return 0, fmt.Errorf("read %s: %w", where, err)
	}
	if branch {
		fc, _, _, err := c.Repositories.GetContents(ctx, owner, name, file, &gogithub.RepositoryContentGetOptions{Ref: cursor.Branch})
		switch {
		case github.IsNotFound(err):
		case err != nil:
			return 0, fmt.Errorf("read %s: %w", where, err)
		case fc == nil:
			return 0, fmt.Errorf("read %s: is a directory", where)
		default:
			if content, err = fc.GetContent(); err != nil {
				return 0, fmt.Errorf("read %s: %w", where, err)
			}
			blob = fc.GetSHA()
		}
	}

	recorded := map[int]bool{}
	var lines []declined
	for _, l := range strings.Split(strings.TrimSpace(content), "\n") {
		f := strings.Fields(l)
		if len(f) != 3 {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		recorded[n] = true
		from := f[1]
		if from == "-" {
			from = ""
		}
		lines = append(lines, declined{pr: n, from: from, to: f[2]})
	}
	added := map[int]bool{}
	for _, d := range found {
		if !recorded[d.pr] {
			lines = append(lines, d)
			added[d.pr] = true
		}
	}
	if len(added) == 0 {
		return 0, nil
	}
	slices.SortStableFunc(lines, func(a, b declined) int { return cmp.Compare(a.pr, b.pr) })
	var b strings.Builder
	for _, d := range lines {
		b.WriteString(d.line())
	}
	nums := make([]string, 0, len(added))
	for n := range added {
		nums = append(nums, "#"+strconv.Itoa(n))
	}
	slices.Sort(nums)
	msg := fmt.Sprintf("okf: record declined bot PRs %s for %s", strings.Join(nums, ", "), source)

	if !branch {
		if err := orphan(ctx, c, owner, name, file, b.String(), msg); err != nil {
			return 0, fmt.Errorf("create %s: %w", where, err)
		}
		return len(added), nil
	}
	opts := &gogithub.RepositoryContentFileOptions{Message: &msg, Content: []byte(b.String()), Branch: new(cursor.Branch)}
	if blob != "" {
		opts.SHA = &blob
	}
	if _, _, err := c.Repositories.UpdateFile(ctx, owner, name, file, opts); err != nil {
		return 0, fmt.Errorf("write %s: %w", where, err)
	}
	return len(added), nil
}

// orphan creates kb-state with one parentless commit holding only file.
func orphan(ctx context.Context, c *gogithub.Client, owner, name, file, content, msg string) error {
	tree, _, err := c.Git.CreateTree(ctx, owner, name, "", []*gogithub.TreeEntry{
		{Path: &file, Mode: new("100644"), Type: new("blob"), Content: &content},
	})
	if err != nil {
		return err
	}
	commit, _, err := c.Git.CreateCommit(ctx, owner, name, gogithub.Commit{Message: &msg, Tree: tree}, nil)
	if err != nil {
		return err
	}
	_, _, err = c.Git.CreateRef(ctx, owner, name, gogithub.CreateRef{Ref: "refs/heads/" + cursor.Branch, SHA: commit.GetSHA()})
	return err
}
