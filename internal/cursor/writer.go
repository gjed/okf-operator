package cursor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// ErrMoved means the cursor no longer holds the commit a plan started from,
// usually because another run advanced it. Re-plan instead of overwriting.
var ErrMoved = errors.New("cursor moved since the plan was made: re-run plan")

// Writer advances cursors on the kb-state branch of KnowledgeRepo. Every write
// is a compare-and-swap, so a concurrent run fails instead of overwriting.
type Writer struct {
	Clients       github.Clients
	KnowledgeRepo string
}

// state is what kb-state holds for one source.
type state struct {
	branch  bool   // kb-state exists
	blob    string // blob SHA of the cursor file, "" when missing
	content string // cursor SHA, "" when missing
}

// Check verifies that the cursor of source still holds from, or, for an
// initial plan, that there is none. It writes nothing.
func (w Writer) Check(ctx context.Context, source, from string, initial bool) error {
	client, owner, name, err := w.client(ctx)
	if err != nil {
		return err
	}
	_, err = w.expect(ctx, client, owner, name, source, from, initial)
	return err
}

// Advance moves the cursor of source from from to to. For an initial plan it
// creates the cursor, and kb-state itself when missing. It writes nothing
// when from equals to.
func (w Writer) Advance(ctx context.Context, source, from, to string, initial bool) error {
	if !ValidSHA(to) {
		return fmt.Errorf("advance cursor for %s: %q is not a full commit SHA", source, to)
	}
	client, owner, name, err := w.client(ctx)
	if err != nil {
		return err
	}
	st, err := w.expect(ctx, client, owner, name, source, from, initial)
	if err != nil || (!initial && from == to) {
		return err
	}
	file := Path(source)
	msg := fmt.Sprintf("okf: advance cursor for %s to %s", source, to)
	where := fmt.Sprintf("cursor %s on %s@%s", file, w.KnowledgeRepo, Branch)
	if !st.branch {
		if err := w.orphan(ctx, client, owner, name, file, to+"\n", msg); err != nil {
			return fmt.Errorf("create %s: %w", where, conflict(err))
		}
		return nil
	}
	opts := &gogithub.RepositoryContentFileOptions{Message: &msg, Content: []byte(to + "\n"), Branch: new(Branch)}
	if st.blob != "" {
		opts.SHA = &st.blob
	}
	if _, _, err := client.Repositories.UpdateFile(ctx, owner, name, file, opts); err != nil {
		return fmt.Errorf("write %s: %w", where, conflict(err))
	}
	return nil
}

// expect reads the cursor and fails with ErrMoved unless it matches the plan.
func (w Writer) expect(ctx context.Context, client *gogithub.Client, owner, name, source, from string, initial bool) (state, error) {
	st, err := w.read(ctx, client, owner, name, source)
	if err != nil {
		return st, err
	}
	where := fmt.Sprintf("cursor %s on %s@%s", Path(source), w.KnowledgeRepo, Branch)
	switch {
	case initial && st.blob != "":
		return st, fmt.Errorf("%s holds %s but the plan is initial: %w", where, st.content, ErrMoved)
	case !initial && st.content != from:
		got := st.content
		if got == "" {
			got = "nothing"
		}
		return st, fmt.Errorf("%s holds %s but the plan starts at %s: %w", where, got, from, ErrMoved)
	}
	return st, nil
}

func (w Writer) read(ctx context.Context, client *gogithub.Client, owner, name, source string) (state, error) {
	file := Path(source)
	where := fmt.Sprintf("cursor %s on %s@%s", file, w.KnowledgeRepo, Branch)
	if _, _, err := client.Git.GetRef(ctx, owner, name, "heads/"+Branch); github.IsNotFound(err) {
		return state{}, nil
	} else if err != nil {
		return state{}, fmt.Errorf("read %s: %w", where, err)
	}
	fc, _, _, err := client.Repositories.GetContents(ctx, owner, name, file, &gogithub.RepositoryContentGetOptions{Ref: Branch})
	if github.IsNotFound(err) {
		return state{branch: true}, nil
	}
	if err != nil {
		return state{}, fmt.Errorf("read %s: %w", where, err)
	}
	if fc == nil {
		return state{}, fmt.Errorf("read %s: is a directory", where)
	}
	content, err := fc.GetContent()
	if err != nil {
		return state{}, fmt.Errorf("read %s: %w", where, err)
	}
	sha := strings.TrimSuffix(content, "\n")
	if !ValidSHA(sha) {
		return state{}, fmt.Errorf("%s is malformed: want one 40-character hex SHA and a newline", where)
	}
	return state{branch: true, blob: fc.GetSHA(), content: sha}, nil
}

// orphan creates kb-state with one parentless commit holding only file.
func (w Writer) orphan(ctx context.Context, client *gogithub.Client, owner, name, file, content, msg string) error {
	tree, _, err := client.Git.CreateTree(ctx, owner, name, "", []*gogithub.TreeEntry{
		{Path: &file, Mode: new("100644"), Type: new("blob"), Content: &content},
	})
	if err != nil {
		return err
	}
	commit, _, err := client.Git.CreateCommit(ctx, owner, name, gogithub.Commit{Message: &msg, Tree: tree}, nil)
	if err != nil {
		return err
	}
	_, _, err = client.Git.CreateRef(ctx, owner, name, gogithub.CreateRef{Ref: "refs/heads/" + Branch, SHA: commit.GetSHA()})
	return err
}

func (w Writer) client(ctx context.Context) (*gogithub.Client, string, string, error) {
	owner, name, _ := strings.Cut(w.KnowledgeRepo, "/")
	client, err := w.Clients.ClientFor(ctx, owner)
	return client, owner, name, err
}

// conflict wraps the 409 and 422 a lost compare-and-swap returns in ErrMoved.
func conflict(err error) error {
	var e *gogithub.ErrorResponse
	if errors.As(err, &e) && e.Response != nil &&
		(e.Response.StatusCode == http.StatusConflict || e.Response.StatusCode == http.StatusUnprocessableEntity) {
		return fmt.Errorf("%w (%w)", ErrMoved, err)
	}
	return err
}
