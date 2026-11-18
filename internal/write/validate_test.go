package write

import (
	"strings"
	"testing"
)

func TestBaseValidator(t *testing.T) {
	cases := map[string]struct {
		path string
		page string
		want string // substring of the error; empty means valid
	}{
		"valid": {
			path: "product/a.md",
			page: "---\ntype: concept\ntitle: A\n---\n# A\n\nBody.\n",
		},
		"reserved index.md": {
			path: "product/index.md",
			page: "---\ntype: concept\n---\n# Index\n\nBody.\n",
			want: "reserved OKF filename",
		},
		"reserved log.md": {
			path: "product/sub/log.md",
			page: "---\ntype: concept\n---\nBody.\n",
			want: "reserved OKF filename",
		},
		"no frontmatter": {
			path: "product/a.md",
			page: "# A\n\nBody.\n",
			want: "must start with a --- line",
		},
		"unclosed frontmatter": {
			path: "product/a.md",
			page: "---\ntype: concept\n# A\n\nBody.\n",
			want: "must start with a --- line",
		},
		"malformed YAML": {
			path: "product/a.md",
			page: "---\ntype: [concept\n---\nBody.\n",
			want: "not valid YAML",
		},
		"non-mapping frontmatter": {
			path: "product/a.md",
			page: "---\n- concept\n- guide\n---\nBody.\n",
			want: "must be a YAML mapping",
		},
		"empty frontmatter": {
			path: "product/a.md",
			page: "---\n---\nBody.\n",
			want: "must be a YAML mapping",
		},
		"missing type": {
			path: "product/a.md",
			page: "---\ntitle: A\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"non-string type number": {
			path: "product/a.md",
			page: "---\ntype: 3\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"non-string type list": {
			path: "product/a.md",
			page: "---\ntype: [concept]\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"non-string type bool": {
			path: "product/a.md",
			page: "---\ntype: true\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"empty type": {
			path: "product/a.md",
			page: "---\ntype: \"\"\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"blank type": {
			path: "product/a.md",
			page: "---\ntype: \"   \"\n---\nBody.\n",
			want: "non-empty string `type`",
		},
		"empty body": {
			path: "product/a.md",
			page: "---\ntype: concept\n---\n",
			want: "body after the frontmatter is empty",
		},
		"whitespace-only body": {
			path: "product/a.md",
			page: "---\ntype: concept\n---\n \n\n",
			want: "body after the frontmatter is empty",
		},
		"closing delimiter at end of input, body empty": {
			path: "product/a.md",
			page: "---\ntype: concept\n---",
			want: "body after the frontmatter is empty",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := BaseValidator{}.Validate(tc.path, []byte(tc.page))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Validate = %v, want nil", err)
			case tc.want != "" && err == nil:
				t.Fatalf("Validate = nil, want an error containing %q", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("Validate = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
