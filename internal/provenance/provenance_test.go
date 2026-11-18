package provenance

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var sampleTime = time.Date(2026, 11, 18, 10, 30, 5, 0, time.UTC)

var sampleFields = Fields{
	SourceRepo:  "acme/api",
	SourceSHA:   "abc0000000000000000000000000000000000001",
	SourcePaths: []string{"api/handler.go", "api/routes.go"},
	UpdatedAt:   sampleTime,
	GeneratedBy: "okf-operator/1.2.3/m1",
}

func keyOrder(t *testing.T, page []byte) []string {
	t.Helper()
	fm, _, err := split(page)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	m, err := mapping(fm)
	if err != nil {
		t.Fatalf("mapping: %v", err)
	}
	var out []string
	for i := 0; i < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

func TestParseAndStampErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		page    string
		wantErr error // matched with errors.Is when non-nil
	}{
		{name: "no frontmatter", page: "# Title\n\nbody\n", wantErr: ErrNoFrontmatter},
		{name: "empty page", page: "", wantErr: ErrNoFrontmatter},
		{name: "CRLF opening not accepted", page: "---\r\nsource_repo: a/b\r\n---\r\nbody\n", wantErr: ErrNoFrontmatter},
		{name: "unclosed frontmatter", page: "---\nsource_repo: a/b\nbody\n", wantErr: ErrNoFrontmatter},
		{name: "malformed YAML", page: "---\nsource_repo: [a\n---\nbody\n"},
		{name: "non-mapping frontmatter", page: "---\n- a\n- b\n---\nbody\n"},
		{name: "scalar frontmatter", page: "---\njust text\n---\nbody\n"},
		{name: "source_paths is a string", page: "---\nsource_paths: api/handler.go\n---\n"},
		{name: "source_paths has a mapping item", page: "---\nsource_paths:\n  - a: b\n---\n"},
		{name: "updated_at is not a timestamp", page: "---\nupdated_at: yesterday\n---\n"},
		{name: "updated_at is a number", page: "---\nupdated_at: 42\n---\n"},
		{name: "repeated provenance key", page: "---\nsource_sha: a\nsource_sha: b\n---\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Parse([]byte(tt.page))
			if err == nil {
				t.Fatal("Parse: want error, got nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Parse: got %v, want %v", err, tt.wantErr)
			}
			_, err = Stamp([]byte(tt.page), sampleFields)
			if err == nil {
				t.Fatal("Stamp: want error, got nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Stamp: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseAbsentKeysAreZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		page string
	}{
		{name: "empty frontmatter", page: "---\n---\nbody\n"},
		{name: "no provenance keys", page: "---\ntitle: Hello\n---\nbody\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, body, err := Parse([]byte(tt.page))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(f, Fields{}) {
				t.Errorf("Fields = %+v, want zero", f)
			}
			if string(body) != "body\n" {
				t.Errorf("body = %q, want %q", body, "body\n")
			}
		})
	}
}

func TestStampSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		page      string
		wantOrder []string
		check     func(t *testing.T, out string)
	}{
		{
			name:      "absent keys appended in fixed order",
			page:      "---\ntitle: Hello\n---\nbody\n",
			wantOrder: []string{"title", KeySourceRepo, KeySourceSHA, KeySourcePaths, KeyUpdatedAt, KeyGeneratedBy},
		},
		{
			name:      "model-written provenance overwritten in place",
			page:      "---\nsource_sha: deadbeef\nowner: team-a\ngenerated_by: model\n---\nbody\n",
			wantOrder: []string{KeySourceSHA, "owner", KeyGeneratedBy, KeySourceRepo, KeySourcePaths, KeyUpdatedAt},
			check: func(t *testing.T, out string) {
				if strings.Contains(out, "deadbeef") {
					t.Errorf("model source_sha survived:\n%s", out)
				}
				if !strings.Contains(out, "owner: team-a") {
					t.Errorf("custom key lost:\n%s", out)
				}
			},
		},
		{
			name:      "custom key keeps original position between provenance keys",
			page:      "---\nsource_repo: old/repo\nowner: team-a\nsource_sha: x\n---\n",
			wantOrder: []string{KeySourceRepo, "owner", KeySourceSHA, KeySourcePaths, KeyUpdatedAt, KeyGeneratedBy},
		},
		{
			name:      "empty frontmatter gets all keys",
			page:      "---\n---\n",
			wantOrder: []string{KeySourceRepo, KeySourceSHA, KeySourcePaths, KeyUpdatedAt, KeyGeneratedBy},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := Stamp([]byte(tt.page), sampleFields)
			if err != nil {
				t.Fatalf("Stamp: %v", err)
			}
			if got := keyOrder(t, out); !reflect.DeepEqual(got, tt.wantOrder) {
				t.Errorf("key order = %v, want %v\n%s", got, tt.wantOrder, out)
			}
			if tt.check != nil {
				tt.check(t, string(out))
			}
		})
	}
}

func TestStampParseRoundTrip(t *testing.T) {
	t.Parallel()
	// The body contains "---" lines: only the first closing delimiter ends
	// the frontmatter, so they must survive as body text.
	body := "# Page\n\n---\nnot frontmatter\n---\n\ntext without final newline"
	tests := []struct {
		name   string
		page   string
		fields Fields
	}{
		{
			name:   "bare page",
			page:   "---\ntitle: Hi\n---\n" + body,
			fields: sampleFields,
		},
		{
			name:   "all-digit SHA stays a string",
			page:   "---\n---\n" + body,
			fields: Fields{SourceRepo: "a/b", SourceSHA: "1234567890123456789012345678901234567890", SourcePaths: []string{"x"}, UpdatedAt: sampleTime, GeneratedBy: "okf-operator/1/m"},
		},
		{
			name:   "no source paths",
			page:   "---\n---\n" + body,
			fields: Fields{SourceRepo: "a/b", SourceSHA: "abc", UpdatedAt: sampleTime, GeneratedBy: "okf-operator/1/m"},
		},
		{
			name:   "sub-second and non-UTC time normalised",
			page:   "---\n---\n" + body,
			fields: Fields{UpdatedAt: time.Date(2026, 11, 18, 12, 0, 0, 900_000_000, time.FixedZone("CET", 3600))},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := Stamp([]byte(tt.page), tt.fields)
			if err != nil {
				t.Fatalf("Stamp: %v", err)
			}
			got, gotBody, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse: %v\n%s", err, out)
			}
			want := tt.fields
			want.UpdatedAt = want.UpdatedAt.Truncate(time.Second).UTC()
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse fields = %+v, want %+v\n%s", got, want, out)
			}
			if !bytes.Equal(gotBody, []byte(body)) {
				t.Errorf("body = %q, want %q", gotBody, body)
			}
		})
	}
}

func TestStampKeepsBodyByteIdentical(t *testing.T) {
	t.Parallel()
	body := []byte("line\r\nwith CRLF\n---\ntrailing spaces   \n")
	page := append([]byte("---\ntitle: x\n---\n"), body...)
	out, err := Stamp(page, sampleFields)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if !bytes.HasSuffix(out, body) {
		t.Errorf("output does not end with the original body:\n%q", out)
	}
	if !bytes.HasPrefix(out, []byte("---\n")) {
		t.Errorf("output does not open with ---:\n%q", out)
	}
}
