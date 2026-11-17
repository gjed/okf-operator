// Package cursor reads the last processed commit of each source repo from the
// kb-state branch of the knowledge repo. Writing cursors belongs to propose.
package cursor

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// Branch is the knowledge repo branch that holds operator state.
const Branch = "kb-state"

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Path is the cursor file for source repo owner/name.
func Path(source string) string { return "cursors/" + source }

// Reader reads cursors from KnowledgeRepo (owner/name) through the contents API.
type Reader struct {
	Clients       github.Clients
	KnowledgeRepo string
}

// Read returns the cursor SHA for source. A 404 on the file or on the branch
// means no cursor (found is false); any other failure is an error, so an outage
// never looks like a first run.
func (r Reader) Read(ctx context.Context, source string) (sha string, found bool, err error) {
	owner, name, _ := strings.Cut(r.KnowledgeRepo, "/")
	client, err := r.Clients.ClientFor(ctx, owner)
	if err != nil {
		return "", false, err
	}
	file := Path(source)
	where := fmt.Sprintf("cursor %s on %s@%s", file, r.KnowledgeRepo, Branch)
	fc, _, _, err := client.Repositories.GetContents(ctx, owner, name, file, &gogithub.RepositoryContentGetOptions{Ref: Branch})
	if github.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", where, err)
	}
	if fc == nil {
		return "", false, fmt.Errorf("read %s: is a directory", where)
	}
	content, err := fc.GetContent()
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", where, err)
	}
	sha = strings.TrimSuffix(content, "\n")
	if !shaRE.MatchString(sha) {
		return "", false, fmt.Errorf("%s is malformed: want one 40-character hex SHA and a newline", where)
	}
	return sha, true, nil
}
