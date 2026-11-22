package plan

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestDigest(t *testing.T) {
	compact := `{"version":1,"source":"acme/api","bundle":"product","from":"a","to":"b","initial":false,"changed_paths":[],` +
		`"pages":[{"path":"product/api.md","state":"update","type":"","source_paths":["api/x.go"]}],"unrouted":[],"excluded":[]}`
	spaced := "{\n  \"version\": 1, \"source\": \"acme/api\", \"bundle\": \"product\", \"from\": \"a\", \"to\": \"b\", \"initial\": false,\n" +
		"  \"changed_paths\": [], \"pages\": [ {\"path\": \"product/api.md\", \"state\": \"update\", \"type\": \"\", \"source_paths\": [\"api/x.go\"]} ],\n" +
		"  \"unrouted\": [], \"excluded\": []\n}"
	digest := func(doc string) string {
		t.Helper()
		var p Plan
		if err := json.Unmarshal([]byte(doc), &p); err != nil {
			t.Fatal(err)
		}
		d, err := Digest(&p)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	a, b := digest(compact), digest(spaced)
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(a) || a != b {
		t.Fatalf("digests %q and %q: want one sha256 digest for both formattings", a, b)
	}
	var p Plan
	_ = json.Unmarshal([]byte(compact), &p)
	p.Pages[0].SourcePaths = []string{"api/y.go"}
	if d, _ := Digest(&p); d == a {
		t.Error("a changed source path kept the digest")
	}
}
