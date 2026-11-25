package pathlang

import "testing"

func sampleAlphabet() ([]string, []string) {
	roots := []string{"ROOT", "TENANT"}
	comps := []string{"a", "b", "c"}
	return BuildAlphabet(roots, comps), comps
}

func TestGrantShapesAndAlgebra(t *testing.T) {
	alpha, comps := sampleAlphabet()

	self, err := BuildSelf(alpha, comps, "ROOT", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := BuildChild(alpha, comps, "ROOT", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	desc, err := BuildDesc(alpha, comps, "ROOT", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}

	// Direct child must be a strict descendant, and self neither.
	if !Subset(child, desc) {
		t.Fatal("child language not contained in descendant language")
	}
	if Subset(self, child) || Subset(self, desc) {
		t.Fatal("self must not be contained in child/descendant")
	}
	if !IsEmpty(Intersect(self, child)) || !IsEmpty(Intersect(self, desc)) {
		t.Fatal("self overlaps child/descendant")
	}

	// A component name repeated from the prefix is still a legal child name.
	repeat, err := BuildChild(alpha, comps, "ROOT", []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !repeat.Accepts([]string{"ROOT", "a", "a"}) {
		t.Fatal("repeated component name not accepted as child")
	}
	if repeat.Accepts([]string{"ROOT", "a", "a", "b"}) {
		t.Fatal("grandchild accepted as direct child")
	}

	// Descendant covers arbitrary depth and rejects the directory itself.
	if !desc.Accepts([]string{"ROOT", "a", "b", "c", "b"}) {
		t.Fatal("deep descendant rejected")
	}
	if desc.Accepts([]string{"ROOT", "a"}) {
		t.Fatal("directory itself accepted as descendant")
	}

	// Union/intersection/complement identities decided by DFA emptiness.
	union := Union(child, self)
	if !Subset(self, union) || !Subset(child, union) {
		t.Fatal("union does not over-approximate operands")
	}
	if !IsEmpty(Intersect(Complement(union), union)) {
		t.Fatal("language and its complement intersect")
	}
	if !Equal(Intersect(union, desc), child) {
		t.Fatal("(self ∪ child) ∩ desc != child")
	}
	if IsEmpty(Universe(alpha)) {
		t.Fatal("universe reported empty")
	}
	if !IsEmpty(Empty(alpha)) {
		t.Fatal("empty reported non-empty")
	}
}

func TestRootAndTenantAlias(t *testing.T) {
	alpha, comps := sampleAlphabet()
	fromRoot, _ := BuildChild(alpha, comps, "ROOT", nil)
	fromTenant, _ := BuildChild(alpha, comps, "TENANT", nil)
	if !IsEmpty(Intersect(fromRoot, fromTenant)) {
		t.Fatal("root and tenant alias languages overlap")
	}
	if fromRoot.Accepts([]string{"TENANT", "a"}) {
		t.Fatal("root grant accepted a tenant-alias resource")
	}
	if !fromTenant.Accepts([]string{"TENANT", "c"}) {
		t.Fatal("tenant alias grant rejected")
	}
}

func TestMinimizePreservesLanguage(t *testing.T) {
	alpha, comps := sampleAlphabet()
	a, _ := BuildDesc(alpha, comps, "ROOT", []string{"a"})
	b, _ := BuildChild(alpha, comps, "ROOT", []string{"a"})
	combined := Minimize(Union(a, Intersect(b, a)))
	if !Equal(combined, a) {
		t.Fatal("minimization changed the language")
	}
	if combined.States > a.States {
		t.Fatal("minimized automaton grew")
	}
}

func TestNormalizeRejects(t *testing.T) {
	for _, bad := range [][]string{{""}, {".."}, {"."}, {"a/b"}, {"a\\b"}} {
		if _, err := Normalize(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
}
