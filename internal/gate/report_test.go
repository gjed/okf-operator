package gate

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixtureReport() *Report {
	r := &Report{Version: Version, PR: 42, HeadSHA: headSHA}
	r.Add(newResult(RuleProvenance, nil))
	r.Add(newResult(RuleRoutedSet, nil))
	r.Add(newResult(RuleAllowlist, []string{`product/z.md: type "Runbook" is not allowed`, `product/a.md: type "Runbook" is not allowed`}))
	r.Add(newResult(RuleChangeShape, nil))
	r.Add(newResult(RuleSize, nil))
	r.Add(newResult(RuleLinks, nil))
	return r
}

func TestReportGolden(t *testing.T) {
	var first bytes.Buffer
	if err := Encode(&first, fixtureReport()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		var again bytes.Buffer
		if err := Encode(&again, fixtureReport()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Bytes(), again.Bytes()) {
			t.Fatal("repeated encoding differs")
		}
	}
	const golden = "testdata/report.golden.json"
	if *update {
		if err := os.WriteFile(golden, first.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), want) {
		t.Errorf("report differs from %s:\n%s", golden, first.String())
	}
}

func TestLaterInputsCanOnlyBlock(t *testing.T) {
	r := fixtureReport()
	if r.Pass {
		t.Fatal("fixture report must fail")
	}
	r.Add(Result{Name: "review", Pass: true})
	if r.Pass {
		t.Error("a passing entry turned a failed report into a pass")
	}

	ok := &Report{}
	ok.Add(newResult(RuleSize, nil))
	if !ok.Pass {
		t.Fatal("all rules pass, report must pass")
	}
	ok.Add(Result{Name: "review", Pass: false})
	if ok.Pass {
		t.Error("a failing entry with no details did not block")
	}
	ok.Rules[1].Pass = true
	ok.Rules[1].Details = []string{"blocked"}
	ok.Finalize()
	if ok.Pass || ok.Rules[1].Pass {
		t.Error("an entry with details passed")
	}

	if empty := (&Report{}); func() bool { empty.Finalize(); return empty.Pass }() {
		t.Error("a report with no rules passed")
	}
}
