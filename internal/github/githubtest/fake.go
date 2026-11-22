// Package githubtest is an in-memory fake of the GitHub REST endpoints
// okf-operator calls, for tests.
package githubtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TokenRequest is one recorded POST /app/installations/{id}/access_tokens.
type TokenRequest struct {
	InstallationID string
	Auth           string
	Body           map[string]any
}

// Fake serves the GitHub endpoints. Configure the exported maps before use.
type Fake struct {
	*httptest.Server

	// InstallationToken is returned by every access_tokens request.
	InstallationToken string

	// AppSlug is returned by GET /app.
	AppSlug string

	// FailStatus makes requests to a URL path answer with that status.
	// A key "METHOD /path" matches only that method.
	FailStatus map[string]int

	// EchoAuth makes FailStatus answers quote the request's Authorization
	// header, like a misbehaving proxy, to test redaction.
	EchoAuth bool

	// TruncateTrees makes tree responses report truncated: true.
	TruncateTrees bool

	mu            sync.Mutex
	hook          func(r *http.Request)
	tokenRequests []TokenRequest
	auths         []string
	requests      []string
	git           map[string]*gitRepo // "owner/name" -> objects, refs, pulls
	pullSeq       int
	repos         map[string][2]string  // "owner/name" -> default branch, head SHA
	compares      map[string][]byte     // "owner/name base...head" -> body
	commitPulls   map[string][]byte     // "owner/name sha" -> body
	commits       map[string]bool       // "owner/name@sha" -> exists
	pulls         map[string]Pull       // "owner/name#n" -> pull request
	pullFiles     map[string][]PullFile // "owner/name#n" -> changed files
	reviews       map[string][]Review   // "owner/name#n" -> posted reviews
}

// Review is one recorded POST /repos/{repo}/pulls/{n}/reviews.
type Review struct {
	CommitID string `json:"commit_id"`
	Body     string `json:"body"`
	Event    string `json:"event"`
	Auth     string `json:"-"`
}

// Reviews returns the reviews posted on pull request number n of repo.
func (f *Fake) Reviews(repo string, n int) []Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.reviews[repo+"#"+strconv.Itoa(n)]...)
}

// Pull is the part of a pull request the fake serves.
type Pull struct {
	Head    string // head commit SHA
	BaseRef string // base branch name
	// ChangedFiles is the PR's changed_files count. Zero means the number of
	// files set with SetPullFiles.
	ChangedFiles int
}

// PullFile is one entry of GET /repos/{repo}/pulls/{n}/files.
type PullFile struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	PreviousFilename string `json:"previous_filename,omitempty"`
}

// SetCommit declares that sha exists in repo, so the SHA media type commit
// endpoint answers it.
func (f *Fake) SetCommit(repo, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commits[repo+"@"+sha] = true
}

// SetPull stores pull request number n of repo.
func (f *Fake) SetPull(repo string, n int, p Pull) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulls[repo+"#"+strconv.Itoa(n)] = p
}

// SetPullFiles stores the changed files of pull request number n of repo.
func (f *Fake) SetPullFiles(repo string, n int, files []PullFile) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullFiles[repo+"#"+strconv.Itoa(n)] = files
}

// SetRepo declares a repo with its default branch and the SHA at its head.
func (f *Fake) SetRepo(repo, defaultBranch, head string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repo] = [2]string{defaultBranch, head}
}

// SetCompare stores the response body of GET /repos/{repo}/compare/{base}...{head}.
func (f *Fake) SetCompare(repo, base, head string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compares[repo+" "+base+"..."+head] = body
}

// SetCommitPulls stores the response body of GET /repos/{repo}/commits/{sha}/pulls.
// A commit without one answers an empty list.
func (f *Fake) SetCommitPulls(repo, sha string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitPulls[repo+" "+sha] = body
}

// New starts a fake and closes it when the test ends. Its URL ends in "/",
// ready for GITHUB_API_URL.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		InstallationToken: "ghs_fakeInstallationToken0123456789",
		AppSlug:           "okf-writer",
		FailStatus:        map[string]int{},
		git:               map[string]*gitRepo{},
		repos:             map[string][2]string{},
		compares:          map[string][]byte{},
		commitPulls:       map[string][]byte{},
		commits:           map[string]bool{},
		pulls:             map[string]Pull{},
		pullFiles:         map[string][]PullFile{},
		reviews:           map[string][]Review{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", f.installation)
	mux.HandleFunc("GET /app", f.app)
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", f.accessToken)
	mux.HandleFunc("GET /repos/{owner}/{repo}/contents/{path...}", f.contents)
	mux.HandleFunc("PUT /repos/{owner}/{repo}/contents/{path...}", f.putContents)
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/blobs", f.createBlob)
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/trees", f.createTree)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/trees/{sha}", f.getTree)
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/commits", f.createCommit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/commits/{sha}", f.getCommit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/ref/{ref...}", f.getRef)
	mux.HandleFunc("POST /repos/{owner}/{repo}/git/refs", f.createRef)
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/git/refs/{ref...}", f.updateRef)
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls", f.listPulls)
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", f.createPull)
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/pulls/{number}", f.editPull)
	mux.HandleFunc("GET /repos/{owner}/{repo}", f.repo)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{ref}", f.commit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/compare/{basehead}", f.compare)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/pulls", f.commitPullRequests)
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{n}", f.pull)
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{n}/files", f.listPullFiles)
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls/{n}/reviews", f.createReview)
	f.Server = httptest.NewServer(f.record(mux))
	t.Cleanup(f.Close)
	return f
}

// APIURL is the base URL to pass as GITHUB_API_URL.
func (f *Fake) APIURL() string { return f.URL + "/" }

// TokenRequests returns the recorded installation token requests.
func (f *Fake) TokenRequests() []TokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TokenRequest(nil), f.tokenRequests...)
}

// Auths returns the Authorization header of every request, in order.
func (f *Fake) Auths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...)
}

// SetHook makes hook run before each request is served, outside the lock,
// so it may call the fake's setters.
func (f *Fake) SetHook(hook func(r *http.Request)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = hook
}

// Requests returns "METHOD /path" of every request, in order.
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// Writes returns the requests that are not GET, in order.
func (f *Fake) Writes() []string {
	var out []string
	for _, r := range f.Requests() {
		if !strings.HasPrefix(r, "GET ") {
			out = append(out, r)
		}
	}
	return out
}

func (f *Fake) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		hook := f.hook
		f.mu.Unlock()
		if hook != nil {
			hook(r)
		}
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		status := f.FailStatus[r.Method+" "+r.URL.Path]
		if status == 0 {
			status = f.FailStatus[r.URL.Path]
		}
		f.mu.Unlock()
		if status != 0 {
			msg := http.StatusText(status)
			if f.EchoAuth {
				msg += " (auth: " + r.Header.Get("Authorization") + ")"
			}
			WriteJSON(w, status, map[string]string{"message": msg})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (f *Fake) installation(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
		NotFound(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"id": 42})
}

func (f *Fake) app(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
		WriteJSON(w, http.StatusUnauthorized, map[string]string{"message": "A JSON web token could not be decoded"})
		return
	}
	f.mu.Lock()
	slug := f.AppSlug
	f.mu.Unlock()
	WriteJSON(w, http.StatusOK, map[string]any{"id": 1, "slug": slug})
}

func (f *Fake) accessToken(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(data, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.tokenRequests = append(f.tokenRequests, TokenRequest{
		InstallationID: r.PathValue("id"),
		Auth:           r.Header.Get("Authorization"),
		Body:           body,
	})
	f.mu.Unlock()
	WriteJSON(w, http.StatusCreated, map[string]any{
		"token":      f.InstallationToken,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

func (f *Fake) lookupRepo(r *http.Request) ([2]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repos[r.PathValue("owner")+"/"+r.PathValue("repo")]
	return repo, ok
}

func (f *Fake) repo(w http.ResponseWriter, r *http.Request) {
	repo, ok := f.lookupRepo(r)
	if !ok {
		NotFound(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"full_name":      r.PathValue("owner") + "/" + r.PathValue("repo"),
		"default_branch": repo[0],
	})
}

// commit answers the SHA media type that GetCommitSHA1 requests, for the
// default branch, the head SHA or a SHA declared with SetCommit.
func (f *Fake) commit(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("owner") + "/" + r.PathValue("repo")
	ref := r.PathValue("ref")
	f.mu.Lock()
	declared := f.commits[name+"@"+ref]
	f.mu.Unlock()
	if !strings.Contains(r.Header.Get("Accept"), "sha") {
		NotFound(w)
		return
	}
	if declared {
		_, _ = io.WriteString(w, ref)
		return
	}
	repo, ok := f.lookupRepo(r)
	if !ok || (ref != repo[0] && ref != repo[1]) {
		NotFound(w)
		return
	}
	_, _ = io.WriteString(w, repo[1])
}

func pullKey(r *http.Request) string {
	return r.PathValue("owner") + "/" + r.PathValue("repo") + "#" + r.PathValue("n")
}

func (f *Fake) pull(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	p, ok := f.pulls[pullKey(r)]
	files := len(f.pullFiles[pullKey(r)])
	f.mu.Unlock()
	if !ok {
		NotFound(w)
		return
	}
	if p.ChangedFiles == 0 {
		p.ChangedFiles = files
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	WriteJSON(w, http.StatusOK, map[string]any{
		"number":        n,
		"changed_files": p.ChangedFiles,
		"head":          map[string]any{"sha": p.Head},
		"base":          map[string]any{"ref": p.BaseRef},
	})
}

// listPullFiles serves one page of a pull request's files and a Link header to the
// next page, as GitHub paginates them.
func (f *Fake) listPullFiles(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	_, ok := f.pulls[pullKey(r)]
	all := f.pullFiles[pullKey(r)]
	f.mu.Unlock()
	if !ok {
		NotFound(w)
		return
	}
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 30
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	start := min((page-1)*per, len(all))
	end := min(start+per, len(all))
	if end < len(all) {
		next := *r.URL
		q := next.Query()
		q.Set("page", strconv.Itoa(page+1))
		q.Set("per_page", strconv.Itoa(per))
		next.RawQuery = q.Encode()
		w.Header().Set("Link", `<`+f.URL+next.RequestURI()+`>; rel="next"`)
	}
	WriteJSON(w, http.StatusOK, append([]PullFile{}, all[start:end]...))
}

// createReview records a review on a pull request set with SetPull.
func (f *Fake) createReview(w http.ResponseWriter, r *http.Request) {
	var rv Review
	data, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(data, &rv); err != nil {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"message": "Problems parsing JSON"})
		return
	}
	rv.Auth = r.Header.Get("Authorization")
	f.mu.Lock()
	_, ok := f.pulls[pullKey(r)]
	if ok {
		f.reviews[pullKey(r)] = append(f.reviews[pullKey(r)], rv)
	}
	f.mu.Unlock()
	if !ok {
		NotFound(w)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"id": 1, "state": "APPROVED", "commit_id": rv.CommitID})
}

func (f *Fake) compare(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	body, ok := f.compares[r.PathValue("owner")+"/"+r.PathValue("repo")+" "+r.PathValue("basehead")]
	f.mu.Unlock()
	if !ok {
		NotFound(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (f *Fake) commitPullRequests(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	body, ok := f.commitPulls[r.PathValue("owner")+"/"+r.PathValue("repo")+" "+r.PathValue("sha")]
	f.mu.Unlock()
	if !ok {
		body = []byte("[]")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// NotFound writes GitHub's 404 body.
func NotFound(w http.ResponseWriter) {
	WriteJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}
