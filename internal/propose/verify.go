package propose

import (
	"crypto/sha1" //nolint:gosec // git object IDs are SHA-1
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Git tree modes.
const (
	modeFile      = "100644"
	modeSymlink   = "120000"
	modeSubmodule = "160000"
)

// maxListed caps the paths one verification error names.
const maxListed = 20

// Verify checks the page-write input contract against the base tree: every
// path under dir except the top-level .git must exist in tree with the same
// content, except the changed pages, which must exist and differ. It returns
// the content and mode of the changed pages.
//
// SIMPLIFIED: without git, ignored files cannot be told apart, so build
// artefacts in the checkout fail the check; file mode changes other than
// symlinks are not detected; the whole checkout is hashed on every run.
// Upgrade path: hash only the bundle directory and .kb/ if repos grow.
func Verify(dir string, tree map[string]Entry, changed map[string]bool) (map[string]File, error) {
	local, err := scan(dir, tree)
	if err != nil {
		return nil, err
	}
	files := map[string]File{}
	var problems []string
	for path, l := range local {
		base, inBase := tree[path]
		switch {
		case changed[path] && !inBase:
			problems = append(problems, path+": changed page is not in the base commit")
		case changed[path] && l.sha == base.SHA:
			problems = append(problems, path+": reported changed but identical to the base commit")
		case changed[path] && l.mode == modeSymlink:
			problems = append(problems, path+": changed page is a symlink")
		case changed[path]:
			files[path] = File{Mode: base.Mode, Content: l.content}
		case !inBase:
			problems = append(problems, path+": not in the base commit and not a changed page")
		case l.sha != base.SHA || (l.mode == modeSymlink) != (base.Mode == modeSymlink):
			problems = append(problems, path+": differs from the base commit and is not a changed page")
		}
	}
	for path, e := range tree {
		if _, ok := local[path]; !ok && e.Mode != modeSubmodule {
			problems = append(problems, path+": missing from the checkout")
		}
	}
	for path := range changed {
		if _, ok := local[path]; !ok {
			if _, inBase := tree[path]; !inBase {
				problems = append(problems, path+": changed page is missing from the checkout")
			}
		}
	}
	if len(problems) == 0 {
		return files, nil
	}
	slices.Sort(problems)
	problems = slices.Compact(problems)
	n := len(problems)
	if n > maxListed {
		problems = append(problems[:maxListed], fmt.Sprintf("and %d more", n-maxListed))
	}
	return nil, fmt.Errorf("knowledge checkout %s does not match the write report against the base commit:\n  %s", dir, strings.Join(problems, "\n  "))
}

type localFile struct {
	mode    string
	sha     string
	content []byte
}

// scan hashes every regular file and symlink under dir as git would,
// skipping the top-level .git and the paths of submodules in tree.
func scan(dir string, tree map[string]Entry) (map[string]localFile, error) {
	out := map[string]localFile{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if e, ok := tree[rel]; ok && e.Mode == modeSubmodule {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			return nil
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = localFile{mode: modeSymlink, sha: blobSHA([]byte(filepath.ToSlash(target)))}
		case d.Type().IsRegular():
			content, err := os.ReadFile(p) //nolint:gosec // p comes from walking dir, not from input
			if err != nil {
				return err
			}
			out[rel] = localFile{mode: modeFile, sha: blobSHA(content), content: content}
		default:
			return fmt.Errorf("%s: unsupported file type %s", rel, d.Type())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan knowledge checkout: %w", err)
	}
	return out, nil
}

// blobSHA is the git blob ID of content.
func blobSHA(content []byte) string {
	h := sha1.New() //nolint:gosec // git object IDs are SHA-1
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}
