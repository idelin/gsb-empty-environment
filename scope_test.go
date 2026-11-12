package netsec

import (
	"strings"
	"testing"
)

// fullBOM builds a complete CycloneDX document. The gpl-transitive component
// is deliberately marked scope=excluded: reachability must still pull it in.
func fullBOM() string {
	return `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "metadata": {
    "component": {
      "bom-ref": "root-app",
      "type": "application",
      "name": "acme-app",
      "version": "1.0.0",
      "purl": "pkg:golang/acme.zone/app@1.0.0"
    }
  },
  "components": [
    {
      "bom-ref": "lib-mit",
      "type": "library",
      "name": "mit-lib",
      "version": "1.2.0",
      "purl": "pkg:golang/acme.zone/mitlib@1.2.0",
      "scope": "required",
      "licenses": [{"license": {"id": "MIT"}}]
    },
    {
      "bom-ref": "lib-gpl",
      "type": "library",
      "name": "gpl-transitive",
      "version": "0.9.1",
      "purl": "pkg:golang/acme.zone/gpltrans@0.9.1",
      "scope": "excluded",
      "licenses": [{"license": {"id": "GPL-3.0-only"}}]
    },
    {
      "bom-ref": "lib-or",
      "type": "library",
      "name": "dual-licensed",
      "version": "2.0.0",
      "purl": "pkg:golang/acme.zone/dual@2.0.0",
      "scope": "optional",
      "licenses": [{"expression": "GPL-3.0-only OR Apache-2.0"}]
    },
    {
      "bom-ref": "lib-unused",
      "type": "library",
      "name": "unused",
      "version": "4.0.0",
      "purl": "pkg:golang/acme.zone/unused@4.0.0",
      "scope": "required",
      "licenses": [{"license": {"id": "AGPL-3.0-only"}}]
    }
  ],
  "dependencies": [
    {"ref": "root-app", "dependencies": ["lib-mit", "lib-or"]},
    {"ref": "lib-mit", "dependencies": ["lib-gpl"]},
    {"ref": "lib-or", "dependencies": ["lib-mit"]}
  ]
}`
}

func mustParseBOM(t *testing.T, data string) *BOM {
	t.Helper()
	bom, err := ParseBOM([]byte(data))
	if err != nil {
		t.Fatalf("ParseBOM failed: %v", err)
	}
	return bom
}

func refSet(comps []Component) map[string]bool {
	set := make(map[string]bool, len(comps))
	for _, comp := range comps {
		set[comp.BOMRef] = true
	}
	return set
}

// TestReachableIncludesExcludedScope is the core regression: a vendor cannot
// hide a runtime dependency by self-reporting scope=excluded.
func TestReachableIncludesExcludedScope(t *testing.T) {
	bom := mustParseBOM(t, fullBOM())

	scope, err := bom.ReachableComponents()
	if err != nil {
		t.Fatalf("ReachableComponents failed: %v", err)
	}
	got := refSet(scope)

	for _, want := range []string{"lib-mit", "lib-gpl", "lib-or"} {
		if !got[want] {
			t.Errorf("reachable scope missing %q", want)
		}
	}
	if got["lib-unused"] {
		t.Errorf("unreachable component lib-unused must not be in scope")
	}
	if got["root-app"] {
		t.Errorf("root deliverable must not appear as a scoped dependency")
	}
}

// TestHiddenGPLViolatesGate proves the excluded-scope GPL dependency is still
// evaluated and fails a permissive-license policy.
func TestHiddenGPLViolatesGate(t *testing.T) {
	bom := mustParseBOM(t, fullBOM())
	policy := NewPolicy("MIT", "Apache-2.0", "BSD-3-Clause")

	report, err := policy.Evaluate(bom)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if report.Allowed() {
		t.Fatal("gate passed, want violation for the hidden GPL dependency")
	}

	var found bool
	for _, violation := range report.Violations {
		if violation.BOMRef == "lib-gpl" {
			found = true
			if !strings.Contains(violation.License, "GPL-3.0-only") {
				t.Errorf("violation license = %q, want GPL-3.0-only", violation.License)
			}
		}
	}
	if !found {
		t.Fatalf("no violation recorded for lib-gpl: %+v", report.Violations)
	}
}

// TestORBranchAllowed verifies OR semantics: one permitted branch is enough.
func TestORBranchAllowed(t *testing.T) {
	bom := mustParseBOM(t, fullBOM())
	policy := NewPolicy("MIT", "GPL-3.0-only")

	report, err := policy.Evaluate(bom)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	var orViolation bool
	for _, violation := range report.Violations {
		if violation.BOMRef == "lib-or" {
			orViolation = true
		}
	}
	if orViolation {
		t.Errorf("dual-licensed component must pass via its allowed OR branch: %+v",
			report.Violations)
	}

	// With neither branch allowed the OR expression must fail.
	strict := NewPolicy("MIT")
	strictReport, err := strict.Evaluate(bom)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	var stillFails bool
	for _, violation := range strictReport.Violations {
		if violation.BOMRef == "lib-or" {
			stillFails = true
		}
	}
	if !stillFails {
		t.Error("OR expression with both branches denied must violate the policy")
	}
}

// TestDuplicateBOMRefRejected ensures ambiguous identity fails the gate.
func TestDuplicateBOMRefRejected(t *testing.T) {
	data := strings.Replace(fullBOM(),
		`"bom-ref": "lib-gpl"`,
		`"bom-ref": "lib-mit"`, 1)
	bom := mustParseBOM(t, data)

	if _, err := bom.ReachableComponents(); err == nil {
		t.Fatal("duplicate bom-ref must produce an error")
	} else if !strings.Contains(err.Error(), "duplicate bom-ref") {
		t.Fatalf("error = %v, want duplicate bom-ref message", err)
	}

	if _, err := NewPolicy("MIT").Evaluate(bom); err == nil {
		t.Fatal("Evaluate must reject a BOM with duplicate bom-refs")
	}
}

// TestSamePURLDifferentVersionsRejected covers the purl/version conflict.
func TestSamePURLDifferentVersionsRejected(t *testing.T) {
	data := strings.Replace(fullBOM(),
		`"purl": "pkg:golang/acme.zone/gpltrans@0.9.1"`,
		`"purl": "pkg:golang/acme.zone/mitlib@9.9.9"`, 1)
	bom := mustParseBOM(t, data)

	_, err := bom.ReachableComponents()
	if err == nil {
		t.Fatal("same purl with different versions must produce an error")
	}
	if !strings.Contains(err.Error(), "purl conflict") {
		t.Fatalf("error = %v, want purl conflict message", err)
	}
}

// TestSamePURLSameVersionAccepted makes sure identical purls are not a false
// positive (e.g. the same package referenced from multiple graph locations).
func TestSamePURLSameVersionAccepted(t *testing.T) {
	data := strings.Replace(fullBOM(),
		`"purl": "pkg:golang/acme.zone/gpltrans@0.9.1"`,
		`"purl": "pkg:golang/acme.zone/mitlib@1.2.0"`, 1)
	bom := mustParseBOM(t, data)

	if _, err := bom.ReachableComponents(); err != nil {
		t.Fatalf("identical purl/version pairs must be accepted: %v", err)
	}
}

// TestDanglingEdgeRejected ensures the walk cannot silently drop a branch.
func TestDanglingEdgeRejected(t *testing.T) {
	data := strings.Replace(fullBOM(),
		`{"ref": "lib-mit", "dependencies": ["lib-gpl"]}`,
		`{"ref": "lib-mit", "dependencies": ["lib-ghost"]}`, 1)
	bom := mustParseBOM(t, data)

	_, err := bom.ReachableComponents()
	if err == nil || !strings.Contains(err.Error(), "unknown ref") {
		t.Fatalf("want unknown ref error, got %v", err)
	}
}

// TestReachabilityIgnoresScopeDirectly asserts the scope field has no effect
// by comparing a document where every component claims scope=excluded.
func TestReachabilityIgnoresScopeDirectly(t *testing.T) {
	data := strings.ReplaceAll(fullBOM(), `"scope": "required"`, `"scope": "excluded"`)
	bom := mustParseBOM(t, data)

	scope, err := bom.ReachableComponents()
	if err != nil {
		t.Fatalf("ReachableComponents failed: %v", err)
	}
	if len(scope) != 3 {
		t.Fatalf("got %d scoped components, want 3 regardless of self-reported scope",
			len(scope))
	}
}

// TestCyclesTerminate checks a cyclic graph still resolves to a fixed set.
func TestCyclesTerminate(t *testing.T) {
	data := strings.Replace(fullBOM(),
		`{"ref": "lib-or", "dependencies": ["lib-mit"]}`,
		`{"ref": "lib-or", "dependencies": ["lib-mit"]},
     {"ref": "lib-gpl", "dependencies": ["lib-or"]}`, 1)
	bom := mustParseBOM(t, data)

	scope, err := bom.ReachableComponents()
	if err != nil {
		t.Fatalf("ReachableComponents failed on cyclic graph: %v", err)
	}
	if len(scope) != 3 {
		t.Fatalf("got %d components, want 3 in cyclic graph", len(scope))
	}
}
