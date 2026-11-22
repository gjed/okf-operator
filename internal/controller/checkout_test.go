package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	typ              byte
}

func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// GitHub archives start with a pax global header holding the commit.
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "abc"}}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Linkname: e.link, Size: int64(len(e.body))}
		if e.typ == tar.TypeDir {
			h.Mode = 0o755
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var goodArchive = []entry{
	{name: "acme-kb-1234567/", typ: tar.TypeDir},
	{name: "acme-kb-1234567/.kb/", typ: tar.TypeDir},
	{name: "acme-kb-1234567/.kb/routing.yaml", body: "version: 1\n", typ: tar.TypeReg},
	{name: "acme-kb-1234567/product/api.md", body: "# API\n", typ: tar.TypeReg},
	{name: "acme-kb-1234567/product/link.md", link: "api.md", typ: tar.TypeSymlink},
}

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	if err := Extract(bytes.NewReader(tarball(t, goodArchive...)), dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "product", "api.md")); err != nil || string(b) != "# API\n" {
		t.Fatalf("api.md = %q, %v", b, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "product", "link.md")); err != nil || target != "api.md" {
		t.Fatalf("link.md -> %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "acme-kb-1234567")); !os.IsNotExist(err) {
		t.Fatal("the archive's top-level directory was kept")
	}
}

func TestExtractRejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	tests := map[string][]entry{
		"dot dot":         {{name: "top/../../evil", body: "x", typ: tar.TypeReg}},
		"absolute":        {{name: "top//etc/evil", body: "x", typ: tar.TypeReg}},
		"through symlink": {{name: "top/out", link: outside, typ: tar.TypeSymlink}, {name: "top/out/evil", body: "x", typ: tar.TypeReg}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			if err := Extract(bytes.NewReader(tarball(t, entries...)), t.TempDir()); err == nil {
				t.Fatal("want an error")
			}
			if _, err := os.Stat(filepath.Join(outside, "evil")); !os.IsNotExist(err) {
				t.Fatal("a file was written outside the checkout")
			}
		})
	}
}

func TestTarballCheckoutFollowsRedirect(t *testing.T) {
	sha := strings.Repeat("d", 40)
	archive := tarball(t, goodArchive...)
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/kb/tarball/"+sha, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		http.Redirect(w, r, "/codeload/acme/kb/legacy.tar.gz/"+sha, http.StatusFound)
	})
	mux.HandleFunc("GET /codeload/acme/kb/legacy.tar.gz/"+sha, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	if err := (TarballCheckout{}).Checkout(context.Background(), tokenClients(t, srv.URL+"/"), "acme/kb", sha, dir); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer ghp_test0123456789" {
		t.Fatalf("tarball request auth = %q", auth)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kb", "routing.yaml")); err != nil {
		t.Fatal(err)
	}

	if err := (TarballCheckout{}).Checkout(context.Background(), tokenClients(t, srv.URL+"/"), "acme/kb", strings.Repeat("e", 40), t.TempDir()); err == nil {
		t.Fatal("want an error for a missing archive")
	}
}
