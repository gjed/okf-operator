// Package provenance reads and writes the provenance frontmatter keys that
// every generated knowledge page carries.
//
// A page is frontmatter followed by a body. Frontmatter starts with a line
// that is exactly "---" followed by LF (CRLF is not accepted), and ends at the
// first following line that is exactly "---". The body is everything after
// that closing line's newline, and it is never modified.
package provenance

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

// Provenance frontmatter keys.
const (
	KeySourceRepo  = "source_repo"
	KeySourceSHA   = "source_sha"
	KeySourcePaths = "source_paths"
	KeyUpdatedAt   = "updated_at"
	KeyGeneratedBy = "generated_by"
	KeyRoutedPaths = "routed_paths"
)

// ErrNoFrontmatter reports a page without a closed "---" frontmatter block.
var ErrNoFrontmatter = errors.New("no frontmatter")

// delimiter is the line that opens and closes frontmatter.
const delimiter = "---"

// keys lists the provenance keys in the order Stamp appends missing ones.
var keys = []string{KeySourceRepo, KeySourceSHA, KeySourcePaths, KeyUpdatedAt, KeyGeneratedBy, KeyRoutedPaths}

// Fields is the provenance a generated page carries.
type Fields struct {
	SourceRepo  string    // source_repo, owner/name
	SourceSHA   string    // source_sha, full 40-char commit SHA
	SourcePaths []string  // source_paths, sorted
	UpdatedAt   time.Time // updated_at, RFC 3339 UTC
	GeneratedBy string    // generated_by, okf-operator/<version>/<model>
	// RoutedPaths is routed_paths, sorted: the source files the page reflects
	// at SourceSHA. It is nil when the key is absent or null (the routed set
	// is unknown) and non-nil, possibly empty, when it is a list.
	RoutedPaths []string
}

// Parse returns the provenance fields of page and the body after its
// frontmatter. Absent keys leave zero values. It fails with ErrNoFrontmatter
// (wrapped) when the page has no closed frontmatter, and with an ordinary
// error when the frontmatter is not a YAML mapping, is malformed, repeats a
// provenance key, or holds a provenance key of the wrong type.
func Parse(page []byte) (Fields, []byte, error) {
	fm, body, err := split(page)
	if err != nil {
		return Fields{}, nil, err
	}
	m, err := mapping(fm)
	if err != nil {
		return Fields{}, nil, err
	}
	var f Fields
	for _, key := range keys {
		v, err := lookup(m, key)
		if err != nil {
			return Fields{}, nil, err
		}
		if v == nil {
			continue
		}
		if err := decode(key, v, &f); err != nil {
			return Fields{}, nil, fmt.Errorf("provenance: %s: %w", key, err)
		}
	}
	return f, body, nil
}

// Type returns the OKF concept `type` of page, trimmed, or "" when the key is
// absent. It fails like Parse on a page without frontmatter or with a
// malformed one, and when `type` is not a string or is repeated.
func Type(page []byte) (string, error) {
	fm, _, err := split(page)
	if err != nil {
		return "", err
	}
	m, err := mapping(fm)
	if err != nil {
		return "", err
	}
	v, err := lookup(m, "type")
	if err != nil || v == nil {
		return "", err
	}
	var t string
	if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Decode(&t) != nil {
		return "", errors.New("provenance: type is not a string")
	}
	return strings.TrimSpace(t), nil
}

// Stamp returns page with f written into its provenance keys. Existing
// provenance keys are replaced in place, missing ones are appended in the
// order of keys, and every other key keeps its position and value. The body is
// copied byte for byte. SourcePaths is written in the order given, so callers
// pass it sorted, and RoutedPaths likewise; a nil RoutedPaths removes the
// routed_paths key. Stamp fails like Parse on a page without frontmatter, a
// malformed frontmatter or a repeated provenance key.
func Stamp(page []byte, f Fields) ([]byte, error) {
	fm, body, err := split(page)
	if err != nil {
		return nil, err
	}
	doc, err := document(fm)
	if err != nil {
		return nil, err
	}
	m := doc.Content[0]
	values := map[string]*yaml.Node{
		KeySourceRepo:  str(f.SourceRepo),
		KeySourceSHA:   str(f.SourceSHA),
		KeySourcePaths: paths(f.SourcePaths),
		KeyUpdatedAt:   {Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: f.UpdatedAt.UTC().Format(time.RFC3339)},
		KeyGeneratedBy: str(f.GeneratedBy),
		KeyRoutedPaths: paths(f.RoutedPaths),
	}
	for _, key := range keys {
		v, err := lookup(m, key)
		if err != nil {
			return nil, err
		}
		if v != nil {
			// Reject a wrong-typed value rather than silently overwriting it,
			// matching Parse; a well-typed model value is overwritten below.
			if err := decode(key, v, &Fields{}); err != nil {
				return nil, fmt.Errorf("provenance: %s: %w", key, err)
			}
		}
		switch {
		case key == KeyRoutedPaths && f.RoutedPaths == nil:
			remove(m, key)
		case v != nil:
			*v = *values[key]
		default:
			m.Content = append(m.Content, str(key), values[key])
		}
	}
	return join(doc, body)
}

// WithoutRoutedPaths returns page without its routed_paths key, or page
// itself when it has none. It fails like Parse.
func WithoutRoutedPaths(page []byte) ([]byte, error) {
	fm, body, err := split(page)
	if err != nil {
		return nil, err
	}
	doc, err := document(fm)
	if err != nil {
		return nil, err
	}
	if v, err := lookup(doc.Content[0], KeyRoutedPaths); err != nil || v == nil {
		return page, err
	}
	remove(doc.Content[0], KeyRoutedPaths)
	return join(doc, body)
}

// join encodes doc as frontmatter followed by body. The line width is
// unlimited, as in yaml v3, so re-stamping never re-wraps existing values.
func join(doc *yaml.Node, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	enc, err := yaml.NewDumper(&buf, yaml.WithV3Defaults(), yaml.WithIndent(2), yaml.WithLineWidth(-1))
	if err != nil {
		return nil, fmt.Errorf("provenance: encode frontmatter: %w", err)
	}
	if err := enc.Dump(doc); err != nil {
		return nil, fmt.Errorf("provenance: encode frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("provenance: encode frontmatter: %w", err)
	}

	out := make([]byte, 0, len(buf.Bytes())+len(body)+2*len(delimiter)+2)
	out = append(out, delimiter+"\n"...)
	out = append(out, buf.Bytes()...)
	out = append(out, delimiter+"\n"...)
	return append(out, body...), nil
}

// split returns the frontmatter between the opening and closing delimiter
// lines and the body after the closing line. The closing line is the first
// line that is exactly "---". A closing line without a trailing newline ends
// the page, so the body is empty.
func split(page []byte) (fm, body []byte, err error) {
	open := delimiter + "\n"
	if !bytes.HasPrefix(page, []byte(open)) {
		return nil, nil, fmt.Errorf("provenance: %w: page does not start with %q", ErrNoFrontmatter, open)
	}
	start := len(open)
	for pos := start; pos <= len(page); {
		end := bytes.IndexByte(page[pos:], '\n')
		line := page[pos:]
		next := len(page) + 1
		if end >= 0 {
			line = page[pos : pos+end]
			next = pos + end + 1
		}
		if string(line) == delimiter {
			if next > len(page) {
				next = len(page)
			}
			return page[start:pos], page[next:], nil
		}
		pos = next
	}
	return nil, nil, fmt.Errorf("provenance: %w: frontmatter is not closed by %q", ErrNoFrontmatter, delimiter)
}

// document decodes frontmatter into a document node whose root is a mapping.
// Empty frontmatter yields an empty mapping.
func document(fm []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return nil, fmt.Errorf("provenance: frontmatter: %w", err)
	}
	if doc.Kind == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("provenance: frontmatter is not a YAML mapping")
	}
	return &doc, nil
}

// mapping returns the root mapping node of frontmatter.
func mapping(fm []byte) (*yaml.Node, error) {
	doc, err := document(fm)
	if err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

// lookup returns the value node of key in mapping m, or nil when absent. It
// fails when key occurs more than once, since which value wins is ambiguous.
func lookup(m *yaml.Node, key string) (*yaml.Node, error) {
	var found *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind != yaml.ScalarNode || k.Value != key {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("provenance: key %s is repeated", key)
		}
		found = m.Content[i+1]
	}
	return found, nil
}

// remove deletes key and its value from mapping m, if present.
func remove(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// decode decodes the value node v of provenance key into f.
func decode(key string, v *yaml.Node, f *Fields) error {
	switch key {
	case KeySourceRepo:
		return v.Decode(&f.SourceRepo)
	case KeySourceSHA:
		return v.Decode(&f.SourceSHA)
	case KeySourcePaths:
		if err := v.Decode(&f.SourcePaths); err != nil {
			return err
		}
		if len(f.SourcePaths) == 0 {
			f.SourcePaths = nil
		}
		return nil
	case KeyUpdatedAt:
		if err := v.Decode(&f.UpdatedAt); err != nil {
			return err
		}
		f.UpdatedAt = f.UpdatedAt.UTC()
		return nil
	case KeyGeneratedBy:
		return v.Decode(&f.GeneratedBy)
	case KeyRoutedPaths:
		var ps []string
		if err := v.Decode(&ps); err != nil {
			return err
		}
		// A null value is an unknown set, like an absent key.
		if ps == nil && v.Tag != "!!null" {
			ps = []string{}
		}
		f.RoutedPaths = ps
		return nil
	}
	return nil
}

// str returns a plain string scalar that the encoder quotes when needed.
func str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// paths returns a block sequence of string scalars.
func paths(ps []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{}}
	for _, p := range ps {
		seq.Content = append(seq.Content, str(p))
	}
	return seq
}
