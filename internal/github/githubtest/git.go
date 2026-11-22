package githubtest

import (
	"crypto/sha1" //nolint:gosec // git object IDs are SHA-1
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Git object modes the fake stores.
const (
	modeFile    = "100644"
	modeSymlink = "120000"
	modeTree    = "040000"
)

// object is one git object. Blob IDs are real git blob SHAs; tree and commit
// IDs only need to be unique.
type object struct {
	kind    string // blob, tree, commit
	content []byte
	entries map[string]entry // tree: name -> entry
	tree    string           // commit
	parents []string         // commit
	message string           // commit
}

type entry struct{ mode, sha string }

// PullRequest is an open or closed pull request in a fake repo.
type PullRequest struct {
	Number      int
	Title, Body string
	Head, Base  string // branch names
	State       string // open or closed
	MergedAt    string // RFC 3339 merge time of a merged PR, else empty
}

type gitRepo struct {
	objects map[string]*object
	refs    map[string]string // "heads/<branch>" -> commit SHA
	pulls   []*PullRequest
}

// BlobSHA is the git blob ID of content.
func BlobSHA(content []byte) string {
	h := sha1.New() //nolint:gosec // git object IDs are SHA-1
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

func hashOf(kind string, v any) string {
	data, _ := json.Marshal(v)
	sum := sha1.Sum(append([]byte(kind+"\x00"), data...)) //nolint:gosec // fake object IDs
	return hex.EncodeToString(sum[:])
}

// gitRepo returns the store of repo, creating it. Callers hold f.mu.
func (f *Fake) gitRepo(repo string) *gitRepo {
	g := f.git[repo]
	if g == nil {
		g = &gitRepo{objects: map[string]*object{}, refs: map[string]string{}}
		f.git[repo] = g
	}
	return g
}

func (g *gitRepo) putBlob(content []byte) string {
	sha := BlobSHA(content)
	g.objects[sha] = &object{kind: "blob", content: content}
	return sha
}

func (g *gitRepo) putTree(entries map[string]entry) string {
	names := slices.Sorted(func(yield func(string) bool) {
		for n := range entries {
			if !yield(n) {
				return
			}
		}
	})
	flat := make([]string, 0, len(names))
	for _, n := range names {
		flat = append(flat, entries[n].mode+" "+n+" "+entries[n].sha)
	}
	sha := hashOf("tree", flat)
	g.objects[sha] = &object{kind: "tree", entries: entries}
	return sha
}

func (g *gitRepo) putCommit(tree string, parents []string, message string) string {
	sha := hashOf("commit", []any{tree, parents, message, len(g.objects)})
	g.objects[sha] = &object{kind: "commit", tree: tree, parents: parents, message: message}
	return sha
}

// flatten lists the blobs and symlinks under tree by full path.
func (g *gitRepo) flatten(tree, prefix string, out map[string]entry) {
	t := g.objects[tree]
	if t == nil || t.kind != "tree" {
		return
	}
	for name, e := range t.entries {
		if e.mode == modeTree {
			g.flatten(e.sha, prefix+name+"/", out)
			continue
		}
		out[prefix+name] = e
	}
}

// build writes nested trees for a flat path -> entry map and returns the root.
func (g *gitRepo) build(files map[string]entry) string {
	root := map[string]entry{}
	sub := map[string]map[string]entry{}
	for p, e := range files {
		dir, rest, nested := strings.Cut(p, "/")
		if !nested {
			root[p] = e
			continue
		}
		if sub[dir] == nil {
			sub[dir] = map[string]entry{}
		}
		sub[dir][rest] = e
	}
	for dir, children := range sub {
		root[dir] = entry{mode: modeTree, sha: g.build(children)}
	}
	return g.putTree(root)
}

func (g *gitRepo) headFiles(branch string) map[string]entry {
	files := map[string]entry{}
	if c := g.objects[g.refs["heads/"+branch]]; c != nil {
		g.flatten(c.tree, "", files)
	}
	return files
}

// commitFiles commits files on top of branch and moves the branch.
func (g *gitRepo) commitFiles(branch string, files map[string]entry, message string) string {
	var parents []string
	if head, ok := g.refs["heads/"+branch]; ok {
		parents = []string{head}
	}
	sha := g.putCommit(g.build(files), parents, message)
	g.refs["heads/"+branch] = sha
	return sha
}

func (g *gitRepo) isAncestor(ancestor, sha string) bool {
	seen := map[string]bool{}
	queue := []string{sha}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s == ancestor {
			return true
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		if c := g.objects[s]; c != nil {
			queue = append(queue, c.parents...)
		}
	}
	return false
}

// SetFile stores a file on a branch of a repo by committing it on top of the
// branch. A branch exists once it has a file.
func (f *Fake) SetFile(repo, ref, path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	files := g.headFiles(ref)
	files[path] = entry{mode: modeFile, sha: g.putBlob([]byte(content))}
	g.commitFiles(ref, files, "set "+path)
}

// SetSymlink stores a symlink to target on a branch of a repo, committed on
// top of the branch like SetFile.
func (f *Fake) SetSymlink(repo, ref, path, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	files := g.headFiles(ref)
	files[path] = entry{mode: modeSymlink, sha: g.putBlob([]byte(target))}
	g.commitFiles(ref, files, "link "+path)
}

// Ref returns the commit SHA a branch points at, or "" if it does not exist.
func (f *Fake) Ref(repo, branch string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gitRepo(repo).refs["heads/"+branch]
}

// Files returns the content of every file in the tree of a commit or branch.
func (f *Fake) Files(repo, commitOrBranch string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	sha := commitOrBranch
	if s, ok := g.refs["heads/"+commitOrBranch]; ok {
		sha = s
	}
	files := map[string]entry{}
	if c := g.objects[sha]; c != nil {
		g.flatten(c.tree, "", files)
	}
	out := make(map[string]string, len(files))
	for p, e := range files {
		out[p] = string(g.objects[e.sha].content)
	}
	return out
}

// Parents returns the parents of a commit.
func (f *Fake) Parents(repo, sha string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.gitRepo(repo).objects[sha]; c != nil {
		return append([]string(nil), c.parents...)
	}
	return nil
}

// Message returns the message of a commit.
func (f *Fake) Message(repo, sha string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.gitRepo(repo).objects[sha]; c != nil {
		return c.message
	}
	return ""
}

// AddPullRequest adds a pull request and returns its number.
func (f *Fake) AddPullRequest(repo string, p PullRequest) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	p.Number = f.nextPull()
	g.pulls = append(g.pulls, &p)
	return p.Number
}

// PullRequests returns copies of the pull requests of a repo.
func (f *Fake) PullRequests(repo string) []PullRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PullRequest
	for _, p := range f.gitRepo(repo).pulls {
		out = append(out, *p)
	}
	return out
}

func (f *Fake) nextPull() int {
	f.pullSeq++
	return f.pullSeq
}

func repoKey(r *http.Request) string { return r.PathValue("owner") + "/" + r.PathValue("repo") }

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	data, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(data, v); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"message": "Problems parsing JSON"})
		return false
	}
	return true
}

func unprocessable(w http.ResponseWriter, msg string) {
	WriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": msg})
}

func (f *Fake) contents(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	f.mu.Lock()
	g := f.gitRepo(repoKey(r))
	_, ok := g.refs["heads/"+ref]
	files := g.headFiles(ref)
	if c := g.objects[ref]; !ok && c != nil && c.kind == "commit" {
		ok = true
		g.flatten(c.tree, "", files)
	}
	e, found := files[r.PathValue("path")]
	var content []byte
	if found {
		content = g.objects[e.sha].content
	}
	f.mu.Unlock()
	switch {
	case !ok:
		WriteJSON(w, http.StatusNotFound, map[string]string{"message": "No commit found for the ref " + ref})
	case !found:
		NotFound(w)
	default:
		WriteJSON(w, http.StatusOK, map[string]any{
			"type":     "file",
			"encoding": "base64",
			"path":     r.PathValue("path"),
			"sha":      e.sha,
			"size":     len(content),
			"content":  base64.StdEncoding.EncodeToString(content),
		})
	}
}

// putContents creates or updates a file: 409 on a stale sha, 422 when the
// file exists and no sha was sent, 404 when the branch is missing.
func (f *Fake) putContents(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string  `json:"message"`
		Content []byte  `json:"content"`
		SHA     *string `json:"sha"`
		Branch  string  `json:"branch"`
	}
	if !decode(w, r, &body) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	if _, ok := g.refs["heads/"+body.Branch]; !ok {
		WriteJSON(w, http.StatusNotFound, map[string]string{"message": "Branch " + body.Branch + " not found"})
		return
	}
	path := r.PathValue("path")
	files := g.headFiles(body.Branch)
	cur, exists := files[path]
	switch {
	case exists && body.SHA == nil:
		unprocessable(w, `Invalid request. "sha" wasn't supplied.`)
		return
	case exists && *body.SHA != cur.sha, !exists && body.SHA != nil:
		WriteJSON(w, http.StatusConflict, map[string]string{"message": path + " does not match " + derefOr(body.SHA)})
		return
	}
	files[path] = entry{mode: modeFile, sha: g.putBlob(body.Content)}
	commit := g.commitFiles(body.Branch, files, body.Message)
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	WriteJSON(w, status, map[string]any{
		"content": map[string]any{"path": path, "sha": files[path].sha},
		"commit":  map[string]any{"sha": commit},
	})
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *Fake) createBlob(w http.ResponseWriter, r *http.Request) {
	var body struct{ Content, Encoding string }
	if !decode(w, r, &body) {
		return
	}
	content := []byte(body.Content)
	if body.Encoding == "base64" {
		var err error
		if content, err = base64.StdEncoding.DecodeString(body.Content); err != nil {
			unprocessable(w, "bad base64")
			return
		}
	}
	f.mu.Lock()
	sha := f.gitRepo(repoKey(r)).putBlob(content)
	f.mu.Unlock()
	WriteJSON(w, http.StatusCreated, map[string]string{"sha": sha})
}

func (f *Fake) createTree(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseTree string `json:"base_tree"`
		Tree     []struct {
			Path, Mode, Type string
			SHA              *string
			Content          *string
		} `json:"tree"`
	}
	if !decode(w, r, &body) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	files := map[string]entry{}
	if body.BaseTree != "" {
		if t := g.objects[body.BaseTree]; t == nil || t.kind != "tree" {
			unprocessable(w, "base_tree is not a valid tree")
			return
		}
		g.flatten(body.BaseTree, "", files)
	}
	for _, e := range body.Tree {
		switch {
		case e.Content != nil:
			files[e.Path] = entry{mode: e.Mode, sha: g.putBlob([]byte(*e.Content))}
		case e.SHA == nil:
			delete(files, e.Path)
		default:
			if g.objects[*e.SHA] == nil {
				unprocessable(w, "tree.sha "+*e.SHA+" is not a valid blob")
				return
			}
			files[e.Path] = entry{mode: e.Mode, sha: *e.SHA}
		}
	}
	WriteJSON(w, http.StatusCreated, map[string]string{"sha": g.build(files)})
}

func (f *Fake) getTree(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	sha := r.PathValue("sha")
	if c := g.objects[sha]; c != nil && c.kind == "commit" {
		sha = c.tree
	}
	if t := g.objects[sha]; t == nil || t.kind != "tree" {
		NotFound(w)
		return
	}
	var entries []map[string]any
	var walk func(tree, prefix string)
	walk = func(tree, prefix string) {
		for name, e := range g.objects[tree].entries {
			typ := "blob"
			if e.mode == modeTree {
				typ = "tree"
			}
			entries = append(entries, map[string]any{"path": prefix + name, "mode": e.mode, "type": typ, "sha": e.sha})
			if typ == "tree" && r.URL.Query().Get("recursive") != "" {
				walk(e.sha, prefix+name+"/")
			}
		}
	}
	walk(sha, "")
	WriteJSON(w, http.StatusOK, map[string]any{"sha": sha, "tree": entries, "truncated": f.TruncateTrees})
}

func (f *Fake) createCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string   `json:"message"`
		Tree    string   `json:"tree"`
		Parents []string `json:"parents"`
	}
	if !decode(w, r, &body) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	if t := g.objects[body.Tree]; t == nil || t.kind != "tree" {
		unprocessable(w, "Tree SHA does not exist")
		return
	}
	for _, p := range body.Parents {
		if c := g.objects[p]; c == nil || c.kind != "commit" {
			unprocessable(w, "Parent SHA does not exist or is not a commit object")
			return
		}
	}
	sha := g.putCommit(body.Tree, body.Parents, body.Message)
	WriteJSON(w, http.StatusCreated, commitJSON(sha, g.objects[sha]))
}

func commitJSON(sha string, c *object) map[string]any {
	parents := make([]map[string]string, 0, len(c.parents))
	for _, p := range c.parents {
		parents = append(parents, map[string]string{"sha": p})
	}
	return map[string]any{"sha": sha, "message": c.message, "tree": map[string]string{"sha": c.tree}, "parents": parents}
}

func (f *Fake) getCommit(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha := r.PathValue("sha")
	c := f.gitRepo(repoKey(r)).objects[sha]
	if c == nil || c.kind != "commit" {
		NotFound(w)
		return
	}
	WriteJSON(w, http.StatusOK, commitJSON(sha, c))
}

func refJSON(ref, sha string) map[string]any {
	return map[string]any{"ref": "refs/" + ref, "object": map[string]string{"type": "commit", "sha": sha}}
}

func (f *Fake) getRef(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := r.PathValue("ref")
	sha, ok := f.gitRepo(repoKey(r)).refs[ref]
	if !ok {
		NotFound(w)
		return
	}
	WriteJSON(w, http.StatusOK, refJSON(ref, sha))
}

func (f *Fake) createRef(w http.ResponseWriter, r *http.Request) {
	var body struct{ Ref, SHA string }
	if !decode(w, r, &body) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	ref := strings.TrimPrefix(body.Ref, "refs/")
	if _, ok := g.refs[ref]; ok {
		unprocessable(w, "Reference already exists")
		return
	}
	if c := g.objects[body.SHA]; c == nil || c.kind != "commit" {
		unprocessable(w, "Object does not exist")
		return
	}
	g.refs[ref] = body.SHA
	WriteJSON(w, http.StatusCreated, refJSON(ref, body.SHA))
}

func (f *Fake) updateRef(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SHA   string `json:"sha"`
		Force bool   `json:"force"`
	}
	if !decode(w, r, &body) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repoKey(r))
	ref := r.PathValue("ref")
	cur, ok := g.refs[ref]
	switch {
	case !ok:
		unprocessable(w, "Reference does not exist")
		return
	case g.objects[body.SHA] == nil:
		unprocessable(w, "Object does not exist")
		return
	case !body.Force && !g.isAncestor(cur, body.SHA):
		unprocessable(w, "Update is not a fast forward")
		return
	}
	g.refs[ref] = body.SHA
	WriteJSON(w, http.StatusOK, refJSON(ref, body.SHA))
}

func (f *Fake) pullJSON(repo string, g *gitRepo, p *PullRequest) map[string]any {
	var mergedAt any
	if p.MergedAt != "" {
		mergedAt = p.MergedAt
	}
	return map[string]any{
		"merged_at": mergedAt,
		"number":    p.Number,
		"state":     p.State,
		"title":     p.Title,
		"body":      p.Body,
		"html_url":  fmt.Sprintf("%s/%s/pull/%d", f.URL, repo, p.Number),
		"head":      map[string]any{"ref": p.Head, "sha": g.refs["heads/"+p.Head]},
		"base":      map[string]any{"ref": p.Base},
	}
}

func (f *Fake) listPulls(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	_, head, _ := strings.Cut(q.Get("head"), ":")
	repo := repoKey(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	out := []map[string]any{}
	for _, p := range g.pulls {
		if (state != "all" && p.State != state) || (head != "" && p.Head != head) || (q.Get("base") != "" && p.Base != q.Get("base")) {
			continue
		}
		out = append(out, f.pullJSON(repo, g, p))
	}
	WriteJSON(w, http.StatusOK, out)
}

func (f *Fake) createPull(w http.ResponseWriter, r *http.Request) {
	var body struct{ Title, Head, Base, Body string }
	if !decode(w, r, &body) {
		return
	}
	repo := repoKey(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	if _, ok := g.refs["heads/"+body.Head]; !ok {
		unprocessable(w, "head: invalid")
		return
	}
	for _, p := range g.pulls {
		if p.State == "open" && p.Head == body.Head && p.Base == body.Base {
			unprocessable(w, "A pull request already exists for "+body.Head)
			return
		}
	}
	p := &PullRequest{Number: f.nextPull(), Title: body.Title, Body: body.Body, Head: body.Head, Base: body.Base, State: "open"}
	g.pulls = append(g.pulls, p)
	WriteJSON(w, http.StatusCreated, f.pullJSON(repo, g, p))
}

func (f *Fake) editPull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("number"))
	repo := repoKey(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gitRepo(repo)
	for _, p := range g.pulls {
		if p.Number != n {
			continue
		}
		if body.Title != nil {
			p.Title = *body.Title
		}
		if body.Body != nil {
			p.Body = *body.Body
		}
		WriteJSON(w, http.StatusOK, f.pullJSON(repo, g, p))
		return
	}
	NotFound(w)
}
