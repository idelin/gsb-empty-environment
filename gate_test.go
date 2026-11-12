package netsec

import (
	"strings"
	"testing"
)

func compBOM(licenses string) string {
	return `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "metadata": {"component": {"bom-ref": "root", "name": "app"}},
  "components": [
    {
      "bom-ref": "dep",
      "type": "library",
      "name": "dep",
      "version": "1.0.0",
      "purl": "pkg:golang/acme.zone/dep@1.0.0",
      "licenses": [` + licenses + `]
    }
  ],
  "dependencies": [{"ref": "root", "dependencies": ["dep"]}]
}`
}

func evalBOM(t *testing.T, policy Policy, licenses string) Report {
	t.Helper()
	bom := mustParseBOM(t, compBOM(licenses))
	report, err := policy.Evaluate(bom)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	return report
}

func TestANDBothBranchesRequired(t *testing.T) {
	expr := `{"expression": "Apache-2.0 AND MIT"}`

	partial := evalBOM(t, NewPolicy("Apache-2.0"), expr)
	if partial.Allowed() {
		t.Fatal("AND must fail when only one branch is allowed")
	}

	full := evalBOM(t, NewPolicy("Apache-2.0", "MIT"), expr)
	if !full.Allowed() {
		t.Fatalf("AND must pass when both branches are allowed: %+v", full.Violations)
	}
}

func TestWITHExceptionSemantics(t *testing.T) {
	expr := `{"expression": "GPL-2.0-only WITH Classpath-exception-2.0"}`

	baseOnly := evalBOM(t, NewPolicy("GPL-2.0-only"), expr)
	if !baseOnly.Allowed() {
		t.Fatal("base license grant should cover the WITH expression")
	}

	exact := evalBOM(t,
		NewPolicy("GPL-2.0-only WITH Classpath-exception-2.0"), expr)
	if !exact.Allowed() {
		t.Fatal("explicit id WITH exception grant must match")
	}

	denied := evalBOM(t, NewPolicy("MIT"), expr)
	if denied.Allowed() {
		t.Fatal("unrelated policy must not allow the WITH expression")
	}
}

func TestMultipleLicenseChoicesAreOR(t *testing.T) {
	choices := `{"license": {"id": "GPL-3.0-only"}}, {"license": {"id": "MIT"}}`
	report := evalBOM(t, NewPolicy("MIT"), choices)
	if !report.Allowed() {
		t.Fatalf("array entries are alternative licenses, MIT branch must pass: %+v",
			report.Violations)
	}
}

func TestMissingLicenseFailsClosed(t *testing.T) {
	bom := mustParseBOM(t, compBOM(""))
	report, err := NewPolicy("MIT").Evaluate(bom)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if report.Allowed() {
		t.Fatal("component without a license declaration must fail the gate")
	}
	if !strings.Contains(report.Violations[0].Reason, "no license") {
		t.Fatalf("reason = %q, want no-license explanation", report.Violations[0].Reason)
	}
}

func TestParseBOMEnvelope(t *testing.T) {
	if _, err := ParseBOM([]byte(`{"bomFormat":"SPDX","specVersion":"2.3"}`)); err == nil {
		t.Fatal("non-CycloneDX document must be rejected")
	}
	if _, err := ParseBOM([]byte(`{"bomFormat":"CycloneDX"}`)); err == nil {
		t.Fatal("document without specVersion must be rejected")
	}
	if _, err := ParseBOM([]byte("{not json")); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
}

func TestParsePURL(t *testing.T) {
	id, version, ok := parsePURL("pkg:golang/acme.zone/tool@1.2.3?arch=amd64#sub")
	if !ok {
		t.Fatal("valid purl rejected")
	}
	if id.type_ != "golang" || id.namespace != "acme.zone" || id.name != "tool" ||
		id.subpath != "sub" || version != "1.2.3" {
		t.Fatalf("unexpected parse: %+v %q", id, version)
	}

	if _, _, ok := parsePURL(""); ok {
		t.Error("empty purl must report ok=false")
	}
	if _, _, ok := parsePURL("http://not-a-purl"); ok {
		t.Error("non-purl string must report ok=false")
	}

	a, va, _ := parsePURL("pkg:maven/org.x/y@1")
	b, vb, _ := parsePURL("pkg:maven/org.x/y@2")
	if a != b || va == vb {
		t.Fatal("same coordinates with different versions must share an identity")
	}
}

func TestRootlessBOMUsesOrphanRefs(t *testing.T) {
	data := `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "components": [
    {"bom-ref": "a", "name": "a", "licenses": [{"license": {"id": "MIT"}}]},
    {"bom-ref": "b", "name": "b", "licenses": [{"license": {"id": "MIT"}}]}
  ],
  "dependencies": [{"ref": "a", "dependencies": ["b"]}]
}`
	bom := mustParseBOM(t, data)
	scope, err := bom.ReachableComponents()
	if err != nil {
		t.Fatalf("ReachableComponents failed: %v", err)
	}
	if got := refSet(scope); !got["a"] || !got["b"] {
		t.Fatalf("orphan-ref roots must seed the walk, got %v", got)
	}
}
