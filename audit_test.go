package netsec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestXPubWithNonHardenedChildPrivateKeyRecoversWholeSubtree(t *testing.T) {
	tree := testTree(t, "m/0/0", "m/0/1", "m/1/0", "m/2'/0")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, PrivateKey, "m/0"),
	)

	assertExposed(t, report,
		"m", "m/0", "m/0/0", "m/0/1", "m/1", "m/1/0", "m/2'", "m/2'/0",
	)

	root := mustExposure(t, report, "m")
	if !chainHasStep(root.Chain, RuleRecoveredParent, "m") {
		t.Fatalf("root chain does not recover m: %#v", root.Chain)
	}
	if !chainHasSource(root.Chain, "m/0") || !chainHasStep(root.Chain, RuleExportedXPub, "m") {
		t.Fatalf("root chain lacks xpub/child evidence: %#v", root.Chain)
	}

	child := mustExposure(t, report, "m/0")
	if len(child.Chain) != 1 || child.Chain[0].Rule != RuleExportedPrivateKey {
		t.Fatalf("direct child chain = %#v", child.Chain)
	}
}

func TestNonRootParentRecoveryStaysWithinItsSubtree(t *testing.T) {
	tree := testTree(t, "m/0/0", "m/0/1", "m/0/2'", "m/0/2'/0", "m/1")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m/0"),
		material(t, PrivateKey, "m/0/0"),
	)
	assertExposed(t, report, "m/0", "m/0/0", "m/0/1", "m/0/2'", "m/0/2'/0")
	assertNotExposed(t, report, "m", "m/1")
}

func TestHardenedChildPrivateKeyDoesNotRecoverParent(t *testing.T) {
	tree := testTree(t, "m/0", "m/1", "m/2'/0")

	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, PrivateKey, "m/2'"),
	)
	assertExposed(t, report, "m/2'", "m/2'/0")
	assertNotExposed(t, report, "m", "m/0", "m/1")

	report = audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, ExtendedPublicKey, "m/2'"),
		material(t, PrivateKey, "m/2'/0"),
	)
	assertExposed(t, report, "m/2'", "m/2'/0")
	assertNotExposed(t, report, "m", "m/0", "m/1")
	if exposure := mustExposure(t, report, "m/2'"); !chainHasStep(exposure.Chain, RuleRecoveredParent, "m/2'") {
		t.Fatalf("m/2' chain = %#v", exposure.Chain)
	}
}

func TestUpwardPropagationAcrossNonHardenedLevels(t *testing.T) {
	tree := testTree(t, "m/0/0/0", "m/0/1", "m/1", "m/2'")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, PrivateKey, "m/0/0/0"),
	)
	assertExposed(t, report,
		"m", "m/0", "m/0/0", "m/0/0/0", "m/0/1", "m/1", "m/2'",
	)

	chain := mustExposure(t, report, "m").Chain
	var recovered []string
	for _, step := range chain {
		if step.Rule == RuleRecoveredParent {
			recovered = append(recovered, step.Node.String())
		}
	}
	wantRecovered := []string{"m/0/0", "m/0", "m"}
	if !reflect.DeepEqual(recovered, wantRecovered) {
		t.Fatalf("recovered parents = %v, want %v; chain = %#v", recovered, wantRecovered, chain)
	}
}

func TestHardenedEdgeStopsUpwardPropagation(t *testing.T) {
	tree := testTree(t, "m/0", "m/0'", "m/0'/0/0", "m/1")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, ExtendedPublicKey, "m/0'"),
		material(t, PrivateKey, "m/0'/0/0"),
	)
	assertExposed(t, report, "m/0'", "m/0'/0", "m/0'/0/0")
	assertNotExposed(t, report, "m", "m/0", "m/1")
}

func TestXPubOnlyExposesNoPrivateKeys(t *testing.T) {
	tree := testTree(t, "m/0/0", "m/1")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, ExtendedPublicKey, "m/0"),
	)
	if len(report.Exposed) != 0 {
		t.Fatalf("xpub-only report = %#v", report.Exposed)
	}
}

func TestExportedPrivateKeysExposeDescendants(t *testing.T) {
	tree := testTree(t, "m/0/0", "m/0/0'", "m/0/1", "m/1")

	for _, kind := range []MaterialKind{ExtendedPrivateKey, PrivateKey} {
		report := audit(t, tree, material(t, kind, "m/0"))
		assertExposed(t, report, "m/0", "m/0/0", "m/0/0'", "m/0/1")
		assertNotExposed(t, report, "m", "m/1")

		hardenedDescendant := mustExposure(t, report, "m/0/0'")
		if !chainHasStep(hardenedDescendant.Chain, RuleDerivedDescendants, "m/0/0'") {
			t.Fatalf("%s hardened descendant chain = %#v", kind, hardenedDescendant.Chain)
		}
	}
}

func TestUnknownMaterialPathRejected(t *testing.T) {
	tree := testTree(t, "m/0")
	if _, err := AuditExports(tree, []Material{material(t, ExtendedPublicKey, "m/9")}); err == nil {
		t.Fatal("unknown path unexpectedly accepted")
	}
}

func TestReportJSONUsesPathStrings(t *testing.T) {
	tree := testTree(t, "m/0")
	report := audit(t, tree,
		material(t, ExtendedPublicKey, "m"),
		material(t, PrivateKey, "m/0"),
	)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !containsText(string(data), `"path":"m"`) || !containsText(string(data), `"rule":"exported_private_key"`) {
		t.Fatalf("unexpected report JSON: %s", data)
	}
}

func testTree(t *testing.T, paths ...string) *Tree {
	t.Helper()
	tree, err := NewTreeFromStrings(paths)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func material(t *testing.T, kind MaterialKind, path string) Material {
	t.Helper()
	material, err := NewMaterial(kind, MustParsePath(path))
	if err != nil {
		t.Fatal(err)
	}
	return material
}

func audit(t *testing.T, tree *Tree, materials ...Material) *Report {
	t.Helper()
	report, err := AuditExports(tree, materials)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertExposed(t *testing.T, report *Report, paths ...string) {
	t.Helper()
	want := make([]string, len(paths))
	copy(want, paths)
	if got := pathStrings(report.Paths()); !reflect.DeepEqual(got, want) {
		t.Fatalf("exposed = %v, want %v", got, want)
	}
}

func assertNotExposed(t *testing.T, report *Report, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if report.IsExposed(MustParsePath(path)) {
			t.Fatalf("%s unexpectedly exposed", path)
		}
	}
}

func mustExposure(t *testing.T, report *Report, path string) Exposure {
	t.Helper()
	want := MustParsePath(path)
	for _, exposure := range report.Exposed {
		if exposure.Path.Equal(want) {
			return exposure
		}
	}
	t.Fatalf("%s is not exposed", path)
	return Exposure{}
}

func chainHasStep(chain []ChainStep, rule Rule, node string) bool {
	for _, step := range chain {
		if step.Rule == rule && step.Node.String() == node {
			return true
		}
	}
	return false
}

func chainHasSource(chain []ChainStep, source string) bool {
	for _, step := range chain {
		for _, path := range step.Sources {
			if path.String() == source {
				return true
			}
		}
	}
	return false
}

func containsText(text, fragment string) bool {
	for i := 0; i+len(fragment) <= len(text); i++ {
		if text[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
