package netsec

import "testing"

func TestReviewAdditionRejectsNewlyExposedPaths(t *testing.T) {
	tree := testTree(t, "m/0", "m/0/0", "m/1/0")
	existing := []Material{material(t, PrivateKey, "m/1/0")}
	additions := []Material{material(t, ExtendedPublicKey, "m")}

	review, err := ReviewAddition(tree, existing, additions)
	if err != nil {
		t.Fatal(err)
	}
	if review.Accepted {
		t.Fatal("addition unexpectedly accepted")
	}
	if got := pathStrings(review.Baseline.Paths()); len(got) != 1 || got[0] != "m/1/0" {
		t.Fatalf("baseline = %v", got)
	}

	newly := pathStrings(exposuresToPaths(review.NewlyExposed))
	wantNewly := []string{"m", "m/0", "m/0/0", "m/1"}
	if stringSlicesDiffer(newly, wantNewly) {
		t.Fatalf("newly exposed = %v, want %v", newly, wantNewly)
	}
	assertExposed(t, review.Proposed, "m", "m/0", "m/0/0", "m/1", "m/1/0")
}

func TestReviewAdditionAcceptsXNpubWithoutPrivateLeak(t *testing.T) {
	tree := testTree(t, "m/0/0")
	review, err := ReviewAddition(
		tree,
		[]Material{material(t, ExtendedPublicKey, "m")},
		[]Material{material(t, ExtendedPublicKey, "m/0")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Accepted || len(review.NewlyExposed) != 0 {
		t.Fatalf("review = %#v", review)
	}
}

func TestReviewAdditionAcceptsAlreadyExposedPath(t *testing.T) {
	tree := testTree(t, "m/0", "m/1")
	existing := []Material{
		material(t, ExtendedPublicKey, "m"),
		material(t, PrivateKey, "m/0"),
	}
	review, err := ReviewAddition(
		tree,
		existing,
		[]Material{material(t, ExtendedPublicKey, "m/1")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Accepted || len(review.NewlyExposed) != 0 {
		t.Fatalf("review = %#v", review)
	}
}

func TestReviewAdditionRejectsPrivateExport(t *testing.T) {
	tree := testTree(t, "m/0/0")
	review, err := ReviewAddition(
		tree,
		[]Material{material(t, ExtendedPublicKey, "m")},
		[]Material{material(t, PrivateKey, "m/0")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if review.Accepted {
		t.Fatal("private export that changes exposure unexpectedly accepted")
	}
	if !review.Proposed.IsExposed(MustParsePath("m")) {
		t.Fatal("proposed report does not contain collateral root exposure")
	}
}

func exposuresToPaths(exposures []Exposure) []Path {
	paths := make([]Path, len(exposures))
	for i, exposure := range exposures {
		paths[i] = exposure.Path
	}
	return paths
}

func stringSlicesDiffer(left, right []string) bool {
	if len(left) != len(right) {
		return true
	}
	for i := range left {
		if left[i] != right[i] {
			return true
		}
	}
	return false
}
