// Package githubtest is an in-memory fake of the GitHub REST endpoints
// okf-operator calls, for tests.
package githubtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	mu            sync.Mutex
	tokenRequests []TokenRequest
	auths         []string
}

// New starts a fake and closes it when the test ends. Its URL ends in "/",
// ready for GITHUB_API_URL.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{InstallationToken: "ghs_fakeInstallationToken0123456789"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", f.installation)
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", f.accessToken)
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

func (f *Fake) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
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
