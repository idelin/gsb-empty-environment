package admission

import (
	"math/big"
	"testing"
)

// TestDistinctBranchesStayExact guards the exact-rational flow accounting on
// shared successors: two mutually exclusive half-mass branches must keep
// their values independent and sum exactly, with no big.Rat aliasing.
func TestDistinctBranchesStayExact(t *testing.T) {
	half := big.NewRat(1, 2)
	bucketA := deepCopyRat(half)
	bucketB := deepCopyRat(half)
	sum := addRat(bucketA, bucketB)
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("distinct half branches sum = %s, want 1", sum)
	}
	if bucketA.Cmp(half) != 0 || bucketB.Cmp(half) != 0 {
		t.Fatalf("operands mutated by addition: %s, %s", bucketA, bucketB)
	}
}
