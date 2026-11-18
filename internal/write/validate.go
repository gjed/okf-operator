package write

import (
	"bytes"
	"errors"
	"fmt"
	"path"

	"go.yaml.in/yaml/v3"
)

// BaseValidator checks the OKF v0.2 base conformance rules that apply to one
// concept document: a YAML mapping frontmatter with a non-empty string `type`
// (spec §4.1), a non-empty body, and a non-reserved filename (spec §3.1).
//
// SIMPLIFIED: github.com/skosovsky/okf has a full validator but no license
// (https://github.com/skosovsky/okf/issues/13), so it cannot be linked into
// this binary. Upgrade path: an implementation of Validator that calls
// validator.ValidateSource on an overlay of the bundle once it is licensed.
type BaseValidator struct{}

// Validate implements Validator.
func (BaseValidator) Validate(p string, page []byte) error {
	if reserved[path.Base(p)] {
		return fmt.Errorf("%s is a reserved OKF filename, not a concept page", path.Base(p))
	}
	fm, body, ok := frontmatter(page)
	if !ok {
		return errors.New("the page must start with a --- line and close its frontmatter with a --- line")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return fmt.Errorf("the frontmatter is not valid YAML: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return errors.New("the frontmatter must be a YAML mapping")
	}
	var m struct {
		Type any `yaml:"type"`
	}
	if err := doc.Decode(&m); err != nil {
		return fmt.Errorf("the frontmatter cannot be decoded: %w", err)
	}
	if s, ok := m.Type.(string); !ok || len(bytes.TrimSpace([]byte(s))) == 0 {
		return errors.New("the frontmatter must have a non-empty string `type` key")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("the page body after the frontmatter is empty")
	}
	return nil
}
