package netsec

import (
	"errors"
	"math/big"
	"testing"
)

// These small parameters are deliberately easy to factor. They are reference
// values for checking the exercise's algebra only; they are not an example of
// a deployment-safe RSA accumulator.
var (
	refN = big.NewInt(3233) // 53 * 61
	refG = big.NewInt(2)
)

func TestRootAndProofsAgainstReference(t *testing.T) {
	set := []uint32{13, 3, 11, 5, 7}
	acc, err := New(refN, refG, "revocations", 7, set)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	product := referenceProduct(set)
	if got := acc.Product(); got.Cmp(product) != 0 {
		t.Fatalf("P = %s, want %s", got, product)
	}
	wantRoot := new(big.Int).Exp(refG, product, refN)
	if got := acc.Root(); got.Cmp(wantRoot) != 0 {
		t.Fatalf("A = %s, want %s", got, wantRoot)
	}
	wantDigest, err := ComputeRootDigest(refN, refG, wantRoot)
	if err != nil {
		t.Fatal(err)
	}
	if acc.RootDigest() != wantDigest {
		t.Fatal("root digest does not match reference digest")
	}
	if acc.SetID() != "revocations" || acc.Generation() != 7 {
		t.Fatalf("identity = (%q, %d), want (revocations, 7)", acc.SetID(), acc.Generation())
	}
	if got := acc.Elements(); len(got) != len(set) {
		t.Fatalf("element count = %d, want %d", len(got), len(set))
	}

	for _, x := range set {
		x := x
		t.Run("member", func(t *testing.T) {
			proof, err := acc.ProveMembership(x)
			if err != nil {
				t.Fatalf("ProveMembership(%d): %v", x, err)
			}
			wantW := new(big.Int).Exp(refG, new(big.Int).Quo(product, big.NewInt(int64(x))), refN)
			if proof.W.Cmp(wantW) != 0 {
				t.Fatalf("W = %s, want %s", proof.W, wantW)
			}
			if proof.Identity != acc.Identity() {
				t.Fatal("membership proof is not bound to the accumulator identity")
			}
			if err := acc.VerifyMembership(proof); err != nil {
				t.Fatalf("VerifyMembership(%d): %v", x, err)
			}
			if err := VerifyMembershipAt(refN, refG, acc.Root(), acc.SetID(), acc.Generation(), proof); err != nil {
				t.Fatalf("VerifyMembershipAt(%d): %v", x, err)
			}
		})
	}

	sawNegativeCoefficient := false
	for _, x := range []uint32{17, 19, 23, 29} {
		x := x
		t.Run("nonmember", func(t *testing.T) {
			proof, err := acc.ProveNonMembership(x)
			if err != nil {
				t.Fatalf("ProveNonMembership(%d): %v", x, err)
			}
			wantD, wantB, coefficient := referenceNonMembership(t, refN, refG, product, x)
			if coefficient.Sign() < 0 {
				sawNegativeCoefficient = true
			}
			if proof.B.Cmp(wantB) != 0 {
				t.Fatalf("b = %s, want %s", proof.B, wantB)
			}
			if proof.B.Sign() < 0 || proof.B.Cmp(big.NewInt(int64(x))) >= 0 {
				t.Fatalf("b = %s is outside [0, x)", proof.B)
			}
			if proof.D.Cmp(wantD) != 0 {
				t.Fatalf("D = %s, want %s", proof.D, wantD)
			}
			if err := acc.VerifyNonMembership(proof); err != nil {
				t.Fatalf("VerifyNonMembership(%d): %v", x, err)
			}
			if err := VerifyNonMembershipAt(refN, refG, acc.Root(), acc.SetID(), acc.Generation(), proof); err != nil {
				t.Fatalf("VerifyNonMembershipAt(%d): %v", x, err)
			}
		})
	}
	if !sawNegativeCoefficient {
		t.Fatal("test did not exercise a negative Bezout coefficient")
	}

	for _, x := range set {
		if _, err := acc.ProveNonMembership(x); !errors.Is(err, ErrMember) {
			t.Fatalf("ProveNonMembership(%d) error = %v, want ErrMember", x, err)
		}
	}
	if _, err := acc.ProveMembership(17); !errors.Is(err, ErrNotMember) {
		t.Fatalf("ProveMembership(absent) error = %v, want ErrNotMember", err)
	}
}

func TestEmptySetAndUpdates(t *testing.T) {
	acc, err := New(refN, refG, "empty", 0, nil)
	if err != nil {
		t.Fatalf("New(empty): %v", err)
	}
	if acc.Product().Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("empty P = %s, want 1", acc.Product())
	}
	if acc.Root().Cmp(refG) != 0 {
		t.Fatalf("empty A = %s, want g = %s", acc.Root(), refG)
	}
	emptyProof, err := acc.ProveNonMembership(17)
	if err != nil {
		t.Fatalf("empty non-membership: %v", err)
	}
	if emptyProof.B.Cmp(big.NewInt(1)) != 0 || emptyProof.D.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("empty non-membership has b=%s D=%s, want b=1 D=1", emptyProof.B, emptyProof.D)
	}
	if err := acc.VerifyNonMembership(emptyProof); err != nil {
		t.Fatalf("empty VerifyNonMembership: %v", err)
	}

	set := []uint32{3, 5, 7}
	acc, err = New(refN, refG, "revocations", 10, set)
	if err != nil {
		t.Fatal(err)
	}
	oldMember, err := acc.ProveMembership(5)
	if err != nil {
		t.Fatal(err)
	}
	oldNonMember, err := acc.ProveNonMembership(11)
	if err != nil {
		t.Fatal(err)
	}

	updated, err := acc.Add(11)
	if err != nil {
		t.Fatalf("Add(11): %v", err)
	}
	if updated != acc {
		t.Fatal("Add did not advance the receiver")
	}
	if acc.Generation() != 11 {
		t.Fatalf("generation = %d, want 11", acc.Generation())
	}
	wantAfterAdd := new(big.Int).Exp(refG, referenceProduct([]uint32{3, 5, 7, 11}), refN)
	if acc.Root().Cmp(wantAfterAdd) != 0 {
		t.Fatalf("root after add = %s, want %s", acc.Root(), wantAfterAdd)
	}
	if err := acc.VerifyMembership(oldMember); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("old membership proof error = %v, want ErrGenerationMismatch", err)
	}
	if err := acc.VerifyNonMembership(oldNonMember); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("old non-membership proof error = %v, want ErrGenerationMismatch", err)
	}

	// Even if an attacker rewrites the self-reported identity to the new
	// snapshot, the old witnesses must fail the equations under the new root.
	forgedMember := oldMember
	forgedMember.Identity = acc.Identity()
	if err := acc.VerifyMembership(forgedMember); !errors.Is(err, ErrEquationFailed) {
		t.Fatalf("forged old membership error = %v, want ErrEquationFailed", err)
	}
	forgedNonMember := oldNonMember
	forgedNonMember.Identity = acc.Identity()
	if err := acc.VerifyNonMembership(forgedNonMember); !errors.Is(err, ErrEquationFailed) {
		t.Fatalf("forged old non-membership error = %v, want ErrEquationFailed", err)
	}

	// Generate a proof for 13 while it is absent, then delete a different
	// member. The new root must invalidate the old proof even though 13 is
	// still absent from the current set.
	oldAbsent, err := acc.ProveNonMembership(13)
	if err != nil {
		t.Fatalf("non-membership for 13 before delete: %v", err)
	}
	if _, err := acc.Delete(3); err != nil {
		t.Fatalf("Delete(3): %v", err)
	}
	if acc.Generation() != 12 {
		t.Fatalf("generation = %d, want 12", acc.Generation())
	}
	wantAfterDelete := new(big.Int).Exp(refG, referenceProduct([]uint32{5, 7, 11}), refN)
	if acc.Root().Cmp(wantAfterDelete) != 0 {
		t.Fatalf("root after delete = %s, want %s", acc.Root(), wantAfterDelete)
	}
	if err := acc.VerifyMembership(oldMember); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("pre-delete proof error = %v, want ErrGenerationMismatch", err)
	}
	if err := acc.VerifyNonMembership(oldAbsent); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("pre-delete non-membership proof error = %v, want ErrGenerationMismatch", err)
	}
	forgedAbsent := oldAbsent
	forgedAbsent.Identity = acc.Identity()
	if err := acc.VerifyNonMembership(forgedAbsent); !errors.Is(err, ErrEquationFailed) {
		t.Fatalf("forged pre-delete non-membership error = %v, want ErrEquationFailed", err)
	}
	currentMember, err := acc.ProveMembership(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.VerifyMembership(currentMember); err != nil {
		t.Fatalf("current member proof after delete: %v", err)
	}
	deletedIsNonMember, err := acc.ProveNonMembership(3)
	if err != nil {
		t.Fatalf("non-membership after delete: %v", err)
	}
	if err := acc.VerifyNonMembership(deletedIsNonMember); err != nil {
		t.Fatalf("verify non-membership after delete: %v", err)
	}
}

func TestIdentityBindingAndNoSelfReportedRoot(t *testing.T) {
	set := []uint32{3, 5, 7}
	acc, err := New(refN, refG, "set-a", 4, set)
	if err != nil {
		t.Fatal(err)
	}
	member, err := acc.ProveMembership(5)
	if err != nil {
		t.Fatal(err)
	}
	nonMember, err := acc.ProveNonMembership(11)
	if err != nil {
		t.Fatal(err)
	}

	sameRootDifferentID, err := New(refN, refG, "set-b", 4, set)
	if err != nil {
		t.Fatal(err)
	}
	if sameRootDifferentID.Root().Cmp(acc.Root()) != 0 {
		t.Fatal("test setup did not produce the same root")
	}
	if err := sameRootDifferentID.VerifyMembership(member); !errors.Is(err, ErrSetIDMismatch) {
		t.Fatalf("same-root/different-ID membership error = %v, want ErrSetIDMismatch", err)
	}
	if err := sameRootDifferentID.VerifyNonMembership(nonMember); !errors.Is(err, ErrSetIDMismatch) {
		t.Fatalf("same-root/different-ID non-membership error = %v, want ErrSetIDMismatch", err)
	}

	differentGeneration, err := New(refN, refG, "set-a", 5, set)
	if err != nil {
		t.Fatal(err)
	}
	if err := differentGeneration.VerifyMembership(member); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("generation mismatch error = %v, want ErrGenerationMismatch", err)
	}

	tamperedDigest := member
	tamperedDigest.RootDigest[0] ^= 0xff
	if err := acc.VerifyMembership(tamperedDigest); !errors.Is(err, ErrRootDigestMismatch) {
		t.Fatalf("tampered digest error = %v, want ErrRootDigestMismatch", err)
	}

	// Verification is anchored at the caller's root. Supplying the trusted
	// new root with a proof that still names the old root must fail.
	advanced := acc.Clone()
	if _, err := advanced.Add(11); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMembershipProof(refN, refG, advanced.Root(), advanced.Identity(), member); err == nil {
		t.Fatal("old proof was accepted against the new root")
	}
	if err := VerifyNonMembershipProof(refN, refG, advanced.Root(), advanced.Identity(), nonMember); err == nil {
		t.Fatal("old non-membership proof was accepted against the new root")
	}
}

func TestSnapshotHelpersAndLimits(t *testing.T) {
	acc, err := New(refN, refG, "snapshots", 0, []uint32{3, 5})
	if err != nil {
		t.Fatal(err)
	}
	originalRoot := acc.Root()
	originalIdentity := acc.Identity()

	added, err := acc.WithAdded(7)
	if err != nil {
		t.Fatalf("WithAdded: %v", err)
	}
	if acc.Generation() != 0 || acc.Root().Cmp(originalRoot) != 0 || acc.Identity() != originalIdentity {
		t.Fatal("WithAdded mutated its receiver")
	}
	if added.Generation() != 1 {
		t.Fatalf("WithAdded generation = %d, want 1", added.Generation())
	}

	removed, err := added.WithDeleted(7)
	if err != nil {
		t.Fatalf("WithDeleted: %v", err)
	}
	if added.Generation() != 1 || !added.Contains(7) {
		t.Fatal("WithDeleted mutated its receiver")
	}
	if removed.Root().Cmp(originalRoot) != 0 {
		t.Fatal("add-then-delete did not restore the root")
	}
	if removed.Generation() != 2 || removed.Identity() == originalIdentity {
		t.Fatal("same root did not retain a distinct identity generation")
	}

	all := make([]uint32, 0, MaxSetSize)
	for candidate := uint32(3); len(all) < MaxSetSize; candidate++ {
		if IsPrime32(candidate) {
			all = append(all, candidate)
		}
	}
	full, err := New(refN, refG, "full", 0, all)
	if err != nil {
		t.Fatalf("New with MaxSetSize: %v", err)
	}
	var extra uint32
	for candidate := uint32(3); ; candidate++ {
		if IsPrime32(candidate) && !full.Contains(candidate) {
			extra = candidate
			break
		}
	}
	if _, err := full.Add(extra); !errors.Is(err, ErrSetTooLarge) {
		t.Fatalf("Add to full set error = %v, want ErrSetTooLarge", err)
	}

	maxGeneration, err := New(refN, refG, "overflow", ^uint64(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maxGeneration.Add(3); !errors.Is(err, ErrGenerationOverflow) {
		t.Fatalf("generation overflow on add = %v", err)
	}
	maxGenerationWithMember, err := New(refN, refG, "overflow", ^uint64(0), []uint32{3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maxGenerationWithMember.Delete(3); !errors.Is(err, ErrGenerationOverflow) {
		t.Fatalf("generation overflow on delete = %v", err)
	}
}

func referenceProduct(set []uint32) *big.Int {
	product := big.NewInt(1)
	for _, x := range set {
		product.Mul(product, new(big.Int).SetUint64(uint64(x)))
	}
	return product
}

func referenceNonMembership(t *testing.T, n, g, product *big.Int, x uint32) (d, b, coefficient *big.Int) {
	t.Helper()
	xBig := new(big.Int).SetUint64(uint64(x))
	gcd := new(big.Int)
	a := new(big.Int)
	b = new(big.Int)
	gcd.GCD(a, b, xBig, product)
	if gcd.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("test setup: gcd(%d, P) = %s", x, gcd)
	}
	b.Mod(b, xBig)
	coefficient = new(big.Int).Quo(
		new(big.Int).Sub(big.NewInt(1), new(big.Int).Mul(b, product)),
		xBig,
	)
	d = referenceExpSigned(g, coefficient, n)
	return d, b, coefficient
}

func referenceExpSigned(base, exponent, modulus *big.Int) *big.Int {
	effectiveBase := new(big.Int).Set(base)
	abs := new(big.Int).Abs(exponent)
	if exponent.Sign() < 0 {
		effectiveBase = new(big.Int).ModInverse(base, modulus)
	}
	return new(big.Int).Exp(effectiveBase, abs, modulus)
}
