package write

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gjed/okf-operator/internal/provenance"
)

// Knowledge repo config paths, relative to the checkout root.
const (
	ProfilePath   = ".kb/profile.md"
	TemplatesDir  = ".kb/templates"
	maxReferences = 2
)

// reserved are OKF filenames that are never concept pages (spec §3.1).
var reserved = map[string]bool{"index.md": true, "log.md": true}

// checkPagePath rejects a plan page path that is not a clean relative .md path
// inside bundle and outside .kb/. Escapes through symlinks are stopped later
// by os.Root.
func checkPagePath(bundle, p string) error {
	switch {
	case p == "" || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, `\`):
		return fmt.Errorf("plan page %q is not a clean relative path", p)
	case slices.Contains(strings.Split(p, "/"), ".."):
		return fmt.Errorf("plan page %q has a .. segment", p)
	case !strings.HasSuffix(p, ".md"):
		return fmt.Errorf("plan page %q is not a .md file", p)
	case bundle == "" || !strings.HasPrefix(p, bundle+"/"):
		return fmt.Errorf("plan page %q is outside bundle %q", p, bundle)
	case slices.Contains(strings.Split(p, "/"), ".kb"):
		return fmt.Errorf("plan page %q is inside .kb/", p)
	}
	return nil
}

// knowledge is the trusted prompt material read from the checkout.
type knowledge struct {
	profile   string
	templates map[string]string // type -> template
	pages     []refPage         // style-reference candidates, sorted by path
}

type refPage struct {
	path, typ, content string
}

// loadKnowledge reads the profile, the templates and the reference candidates.
// Missing profile and templates are fine.
func loadKnowledge(root *os.Root, bundle string, planned map[string]bool) (knowledge, error) {
	kb := knowledge{templates: map[string]string{}}
	b, err := root.ReadFile(ProfilePath)
	switch {
	case err == nil:
		kb.profile = strings.TrimSpace(string(b))
	case !errors.Is(err, fs.ErrNotExist):
		return kb, fmt.Errorf("read %s: %w", ProfilePath, err)
	}

	tmpl, err := fs.ReadDir(root.FS(), TemplatesDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return kb, fmt.Errorf("read %s: %w", TemplatesDir, err)
	}
	for _, e := range tmpl {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		b, err := root.ReadFile(TemplatesDir + "/" + e.Name())
		if err != nil {
			return kb, fmt.Errorf("read template %s: %w", e.Name(), err)
		}
		kb.templates[name] = strings.TrimSpace(string(b))
	}

	err = fs.WalkDir(root.FS(), bundle, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir() && e.Name() == ".kb":
			return fs.SkipDir
		case !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") || reserved[e.Name()] || planned[p]:
			return nil
		}
		b, err := root.ReadFile(p)
		if err != nil {
			return err
		}
		if typ := pageType(b); typ != "" {
			kb.pages = append(kb.pages, refPage{path: p, typ: typ, content: string(b)})
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return kb, fmt.Errorf("read bundle %s: %w", bundle, err)
	}
	return kb, nil
}

// template returns the template for typ, or "" when there is none.
func (kb knowledge) template(typ string) string {
	return kb.templates[typ]
}

// references returns up to two reference pages: same type first, then the
// rest, each group in path order.
func (kb knowledge) references(typ string) []reference {
	var same, other []reference
	for _, p := range kb.pages {
		r := reference{Path: p.path, Content: p.content}
		if typ != "" && p.typ == typ {
			same = append(same, r)
		} else {
			other = append(other, r)
		}
	}
	refs := append(same, other...)
	return refs[:min(len(refs), maxReferences)]
}

// pageType returns the frontmatter `type` of page, or "" when the page has no
// valid frontmatter or no string type.
func pageType(page []byte) string {
	fm, _, ok := frontmatter(page)
	if !ok {
		return ""
	}
	var m struct {
		Type string `yaml:"type"`
	}
	if yaml.Unmarshal(fm, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Type)
}

// frontmatterKeys returns the top-level keys of page's frontmatter mapping, in
// order, or nil when it has none.
func frontmatterKeys(page []byte) []string {
	fm, _, ok := frontmatter(page)
	if !ok {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(fm, &doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	var keys []string
	for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
		keys = append(keys, doc.Content[0].Content[i].Value)
	}
	return keys
}

// missingKeys lists the top-level frontmatter keys of current, other than the
// provenance keys, that reply lacks.
func missingKeys(current, reply []byte) []string {
	have := map[string]bool{}
	for _, k := range frontmatterKeys(reply) {
		have[k] = true
	}
	var missing []string
	for _, k := range frontmatterKeys(current) {
		if !have[k] && !provenanceKey[k] {
			missing = append(missing, k)
		}
	}
	return missing
}

var provenanceKey = map[string]bool{
	provenance.KeySourceRepo: true, provenance.KeySourceSHA: true, provenance.KeySourcePaths: true,
	provenance.KeyUpdatedAt: true, provenance.KeyGeneratedBy: true,
}

// frontmatter splits page into the YAML between the opening "---" line and
// the next "---" line, and the body after it.
func frontmatter(page []byte) (fm, body []byte, ok bool) {
	rest, ok := bytes.CutPrefix(page, []byte("---\n"))
	if !ok {
		return nil, nil, false
	}
	if b, ok := bytes.CutPrefix(rest, []byte("---\n")); ok {
		return nil, b, true
	}
	i := bytes.Index(rest, []byte("\n---\n"))
	if i < 0 {
		if !bytes.HasSuffix(rest, []byte("\n---")) {
			return nil, nil, false
		}
		return rest[:len(rest)-4], nil, true
	}
	return rest[:i], rest[i+5:], true
}
