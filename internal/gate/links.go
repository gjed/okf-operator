package gate

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	inlineLink = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\s*\)`)
	refDef     = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)
	codeSpan   = regexp.MustCompile("`+[^`]*`+")
	urlScheme  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// linkTargets returns the targets of inline links, images and reference
// definitions in a Markdown page, skipping a leading frontmatter block,
// fenced code blocks and code spans.
//
// SIMPLIFIED: a line-based regex scan, not a CommonMark parser. It misses
// autolinks and HTML links and can misread exotic nesting. Upgrade path: a
// goldmark AST walk behind this function.
func linkTargets(page []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(page))
	sc.Buffer(make([]byte, 0, 64*1024), len(page)+1)
	line, frontmatter, fence := 0, false, ""
	for sc.Scan() {
		text := sc.Text()
		line++
		trimmed := strings.TrimSpace(text)
		// The frontmatter is skipped by its --- delimiters only, not parsed.
		if line == 1 && trimmed == "---" {
			frontmatter = true
			continue
		}
		if frontmatter {
			frontmatter = trimmed != "---"
			continue
		}
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		text = codeSpan.ReplaceAllString(text, "")
		if m := refDef.FindStringSubmatch(text); m != nil {
			out = append(out, unbracket(m[1]))
			continue
		}
		for _, m := range inlineLink.FindAllStringSubmatch(text, -1) {
			out = append(out, unbracket(m[1]))
		}
	}
	return out
}

func unbracket(t string) string {
	return strings.TrimSuffix(strings.TrimPrefix(t, "<"), ">")
}

// relativeTarget strips the query and fragment from a link target and
// reports whether what is left is a relative path to check. Links with a
// scheme, protocol-relative, root-relative and fragment-only links are not.
func relativeTarget(t string) (string, bool) {
	if t == "" || urlScheme.MatchString(t) || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "#") {
		return "", false
	}
	if i := strings.IndexAny(t, "?#"); i >= 0 {
		t = t[:i]
	}
	return t, t != ""
}

// links passes when every relative link in every page resolves, relative to
// the page, to an existing file inside the bundle at the head commit.
func links(ctx context.Context, r Reader, repo, headSHA, bundle string, pages map[string][]byte) (Result, error) {
	var details []string
	exists := map[string]bool{}
	names := make([]string, 0, len(pages))
	for name := range pages {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, page := range names {
		for _, raw := range linkTargets(pages[page]) {
			t, ok := relativeTarget(raw)
			if !ok {
				continue
			}
			if dec, err := url.PathUnescape(t); err == nil {
				t = dec
			}
			target := path.Join(path.Dir(page), t)
			if !strings.HasPrefix(target, bundle+"/") {
				details = append(details, fmt.Sprintf("%s: link %q resolves to %s, outside bundle %s", page, raw, target, bundle))
				continue
			}
			found, seen := exists[target]
			if !seen {
				var err error
				if _, found, err = r.File(ctx, repo, headSHA, target); err != nil {
					return Result{}, err
				}
				exists[target] = found
			}
			if !found {
				details = append(details, fmt.Sprintf("%s: link %q resolves to %s, which is not a file at the head", page, raw, target))
			}
		}
	}
	return newResult(RuleLinks, details), nil
}
