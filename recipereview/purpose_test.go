package recipereview

import "testing"

// TestTwoReadOnlyRecipes confirms the read-only acceptance rule is oriented:
// two read-only recipes do not create an acceptance property, while pairing a
// read-only legacy recipe with an active one does.
func TestTwoReadOnlyRecipes(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	legacy1 := prefixRecipe(30, "legacy-one", "v1", "a", alpha)
	legacy1.ReadOnly = true
	legacy2 := prefixRecipe(31, "legacy-two", "v1", "b", alpha)
	legacy2.ReadOnly = true

	report, err := Review([]*Recipe{legacy1, legacy2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if verdictNamed(report, "readonly:30:31") != nil {
		t.Fatalf("two read-only recipes must not create an acceptance property")
	}

	active := prefixRecipe(32, "active-purpose", "v1", "c", alpha)
	report, err = Review([]*Recipe{legacy1, active}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review with active: %v", err)
	}
	rv := findVerdict(t, report, "readonly:30:32")
	if rv.Kind != VerdictConflict {
		t.Fatalf("active recipe accepts legacy encoding, expected conflict: %+v", rv)
	}
}

// TestSamePurposeVersionsWithoutCompatEntry shows same-purpose versions that
// cannot coexist need no distinctness proof between them.
func TestSamePurposeVersionsWithoutCompatEntry(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	v1 := prefixRecipe(33, "rolling", "v1", "x", alpha)
	v2 := prefixRecipe(34, "rolling", "v2", "y", alpha)
	report, err := Review([]*Recipe{v1, v2}, NewCompatTable(), nil)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	for _, v := range report.Verdicts {
		if v.Property == "compat:33:34" {
			t.Fatalf("compat property generated without a table entry")
		}
	}
	if !report.Safe() {
		t.Fatalf("non-coexisting versions should leave the report safe: %+v", report.Verdicts)
	}
}

// TestDuplicateRecipeRejected ensures ambiguous recipe identity cannot enter a
// review run.
func TestDuplicateRecipeRejected(t *testing.T) {
	alpha := mustAlphabet(t, []byte{0x00, 0x01})
	r1 := prefixRecipe(35, "dup", "v1", "x", alpha)
	r2 := prefixRecipe(35, "other", "v1", "y", alpha)
	if _, err := Review([]*Recipe{r1, r2}, NewCompatTable(), nil); err == nil {
		t.Fatalf("duplicate recipe ID accepted")
	}

	r2.ID = 36
	r2.Purpose = "dup"
	r2.Version = "v1"
	if _, err := Review([]*Recipe{r1, r2}, NewCompatTable(), nil); err == nil {
		t.Fatalf("duplicate purpose/version accepted")
	}
}
