package controller

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gjed/okf-operator/internal/github"
)

// MaxCheckoutBytes caps the uncompressed size of one knowledge checkout.
const MaxCheckoutBytes = 256 << 20

// Checkout materialises repo at commit sha into the empty directory dir.
type Checkout interface {
	Checkout(ctx context.Context, clients github.Clients, repo, sha, dir string) error
}

// TarballCheckout implements Checkout with GET /repos/{owner}/{repo}/tarball/{sha}.
// The image has no git binary.
//
// SIMPLIFIED: GitHub archives honour export-ignore and export-subst, so a
// knowledge repo that uses them fails propose's checkout verification.
// Upgrade path: build the checkout from the git trees and blobs APIs.
type TarballCheckout struct{}

// Checkout implements Checkout.
func (TarballCheckout) Checkout(ctx context.Context, clients github.Clients, repo, sha, dir string) error {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := clients.ClientFor(ctx, owner)
	if err != nil {
		return err
	}
	req, err := c.NewRequest(ctx, http.MethodGet, fmt.Sprintf("repos/%s/%s/tarball/%s", owner, name, sha), nil)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Extract(pr, dir)
		_ = pr.Close()
	}()
	_, err = c.Do(req, pw)
	_ = pw.CloseWithError(err)
	if xerr := <-done; err == nil {
		err = xerr
	}
	if err != nil {
		return fmt.Errorf("download %s@%s: %w", repo, sha, err)
	}
	return nil
}

// Extract unpacks a GitHub tarball into dir, dropping the archive's single
// top-level directory. Only directories, regular files and symlinks are kept;
// an entry that would land outside dir, also through a symlink, or an archive
// over MaxCheckoutBytes, fails.
func Extract(r io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		_, rel, _ := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" {
			continue
		}
		if path.IsAbs(rel) || rel != path.Clean(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("archive entry %q escapes the checkout", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > MaxCheckoutBytes {
				return fmt.Errorf("archive is larger than %d bytes", MaxCheckoutBytes)
			}
			if err := writeFile(root, rel, tr, h.Size); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := mkParent(root, rel); err != nil {
				return err
			}
			if err := root.Symlink(h.Linkname, rel); err != nil {
				return err
			}
		}
	}
}

func mkParent(root *os.Root, rel string) error {
	if d := path.Dir(rel); d != "." {
		return root.MkdirAll(d, 0o750)
	}
	return nil
}

func writeFile(root *os.Root, rel string, r io.Reader, size int64) error {
	if err := mkParent(root, rel); err != nil {
		return err
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
