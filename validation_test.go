package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func TestSetAndParameterValidation(t *testing.T) {
	for _, n := range []*big.Int{nil, big.NewInt(1), big.NewInt(2), big.NewInt(4)} {
		if _, err := New(n, big.NewInt(2), "s", 0, nil); err == nil {
			t.Fatalf("New accepted modulus %v", n)
		}
	}
	for _, g := range []*big.Int{nil, big.NewInt(0), big.NewInt(53), big.NewInt(3233), big.NewInt(-1)} {
		if _, err := New(refN, g, "s", 0, nil); err == nil {
			t.Fatalf("New accepted non-unit generator %v", g)
		}
	}

	for _, element := range []uint32{0, 1, 4, 9, 21, 4294967295} {
		if _, err := New(refN, refG, "s", 0, []uint32{element}); !errors.Is(err, ErrInvalidElement) {
			t.Fatalf("New element %d error = %v, want ErrInvalidElement", element, err)
		}
	}
	if _, err := New(refN, refG, "s", 0, []uint32{3, 5, 3}); !errors.Is(err, ErrDuplicateElement) {
		t.Fatalf("duplicate element error = %v, want ErrDuplicateElement", err)
	}

	tooMany := make([]uint32, 0, MaxSetSize+1)
	for candidate := uint32(3); len(tooMany) < MaxSetSize+1; candidate++ {
		if IsPrime32(candidate) {
			tooMany = append(tooMany, candidate)
		}
	}
	if _, err := New(refN, refG, "s", 0, tooMany); !errors.Is(err, ErrSetTooLarge) {
		t.Fatalf("oversized set error = %v, want ErrSetTooLarge", err)
	}

	acc, err := New(refN, refG, "s", 0, []uint32{3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acc.Add(5); !errors.Is(err, ErrDuplicateElement) {
		t.Fatalf("Add duplicate error = %v, want ErrDuplicateElement", err)
	}
	if acc.Generation() != 0 {
		t.Fatal("failed Add changed generation")
	}
	if _, err := acc.Delete(7); !errors.Is(err, ErrNotMember) {
		t.Fatalf("Delete absent error = %v, want ErrNotMember", err)
	}
	if acc.Generation() != 0 {
		t.Fatal("failed Delete changed generation")
	}
}

func TestCanonicalIntegerRejection(t *testing.T) {
	bad := []string{"", "+1", "01", "-0", " 1", "1 ", "1\t", "1_0", "0x10", "--1", "-01", "١"}
	for _, s := range bad {
		if _, err := ParseCanonicalInt(s); !errors.Is(err, ErrNonCanonical) {
			t.Fatalf("ParseCanonicalInt(%q) error = %v, want ErrNonCanonical", s, err)
		}
	}
	for _, s := range []string{"0", "1", "3233", "-17"} {
		if _, err := ParseCanonicalInt(s); err != nil {
			t.Fatalf("ParseCanonicalInt(%q): %v", s, err)
		}
	}
	if _, err := ParseCanonicalUint32("4294967296"); err == nil {
		t.Fatal("uint32 overflow accepted")
	}
	if _, err := ParseCanonicalUint32("-1"); err == nil {
		t.Fatal("negative uint32 accepted")
	}

	if _, err := NewFromStrings("03233", "2", "s", 0, nil); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical N error = %v, want ErrNonCanonical", err)
	}
	if _, err := NewFromStrings("3233", "2", "s", 0, []string{"03"}); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical element error = %v, want ErrNonCanonical", err)
	}
	if _, err := ParsePrimeElements([]string{"3", "3"}); !errors.Is(err, ErrDuplicateElement) {
		t.Fatalf("duplicate element error = %v, want ErrDuplicateElement", err)
	}
	if _, err := ParsePrimeElements([]string{"4"}); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("composite element error = %v, want ErrInvalidElement", err)
	}
}

func TestProofValidationBoundaries(t *testing.T) {
	set := []uint32{3, 5, 7}
	acc, err := New(refN, refG, "s", 1, set)
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

	nonPrime := member
	nonPrime.X = 9
	if err := acc.VerifyMembership(nonPrime); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("non-prime membership error = %v, want ErrInvalidElement", err)
	}
	nonPrimeNonMember := nonMember
	nonPrimeNonMember.X = 9
	if err := acc.VerifyNonMembership(nonPrimeNonMember); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("non-prime non-membership error = %v, want ErrInvalidElement", err)
	}

	nonUnitW := member
	nonUnitW.W = big.NewInt(53)
	if err := acc.VerifyMembership(nonUnitW); !errors.Is(err, ErrNotUnit) {
		t.Fatalf("W not in unit group error = %v, want ErrNotUnit", err)
	}
	nonUnitD := nonMember
	nonUnitD.D = big.NewInt(53)
	if err := acc.VerifyNonMembership(nonUnitD); !errors.Is(err, ErrNotUnit) {
		t.Fatalf("D not in unit group error = %v, want ErrNotUnit", err)
	}

	negativeB := nonMember
	negativeB.B = big.NewInt(-1)
	if err := acc.VerifyNonMembership(negativeB); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("negative b error = %v, want ErrInvalidRange", err)
	}
	equalB := nonMember
	equalB.B = big.NewInt(11)
	if err := acc.VerifyNonMembership(equalB); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("b == x error = %v, want ErrInvalidRange", err)
	}

	overflowW := member
	overflowW.W = new(big.Int).Add(refN, member.W)
	if err := acc.VerifyMembership(overflowW); !errors.Is(err, ErrNotUnit) {
		t.Fatalf("non-canonical W residue error = %v, want ErrNotUnit", err)
	}

	if _, err := NewMembershipProofFromStrings(member.Identity, member.X, "0"+member.W.String()); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical W string error = %v, want ErrNonCanonical", err)
	}
	if _, err := NewMembershipProofFromStrings(member.Identity, 9, member.W.String()); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("non-prime proof string x error = %v, want ErrInvalidElement", err)
	}
	if _, err := NewNonMembershipProofFromStrings(nonMember.Identity, nonMember.X, "0"+nonMember.D.String(), nonMember.B.String()); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical D string error = %v, want ErrNonCanonical", err)
	}
	if _, err := NewNonMembershipProofFromStrings(nonMember.Identity, 9, nonMember.D.String(), nonMember.B.String()); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("non-prime non-membership string x error = %v, want ErrInvalidElement", err)
	}
	if _, err := NewNonMembershipProofFromStrings(nonMember.Identity, nonMember.X, nonMember.D.String(), "0"+nonMember.B.String()); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical b string error = %v, want ErrNonCanonical", err)
	}
}

func TestProofJSONCanonicalization(t *testing.T) {
	acc, err := New(refN, refG, "json", 2, []uint32{3, 5, 7})
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

	encoded, err := json.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	var decodedMember MembershipProof
	if err := json.Unmarshal(encoded, &decodedMember); err != nil {
		t.Fatalf("membership JSON round trip: %v", err)
	}
	if err := acc.VerifyMembership(decodedMember); err != nil {
		t.Fatalf("decoded membership proof: %v", err)
	}

	encoded, err = json.Marshal(nonMember)
	if err != nil {
		t.Fatal(err)
	}
	var decodedNonMember NonMembershipProof
	if err := json.Unmarshal(encoded, &decodedNonMember); err != nil {
		t.Fatalf("non-membership JSON round trip: %v", err)
	}
	if err := acc.VerifyNonMembership(decodedNonMember); err != nil {
		t.Fatalf("decoded non-membership proof: %v", err)
	}

	digest := member.RootDigestHex()
	badW := fmt.Sprintf(`{"set_id":"json","generation":2,"root_digest":%q,"x":5,"w":"0%s"}`, digest, member.W)
	if err := json.Unmarshal([]byte(badW), &decodedMember); !errors.Is(err, ErrNonCanonical) {
		t.Fatalf("non-canonical JSON W error = %v, want ErrNonCanonical", err)
	}
	negativeB := fmt.Sprintf(`{"set_id":"json","generation":2,"root_digest":%q,"x":11,"d":%q,"b":"-1"}`, digest, nonMember.D.String())
	if err := json.Unmarshal([]byte(negativeB), &decodedNonMember); err != nil {
		t.Fatalf("negative b should decode and then fail verification: %v", err)
	}
	if err := acc.VerifyNonMembership(decodedNonMember); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("negative JSON b error = %v, want ErrInvalidRange", err)
	}
	equalB := fmt.Sprintf(`{"set_id":"json","generation":2,"root_digest":%q,"x":11,"d":%q,"b":"11"}`, digest, nonMember.D.String())
	if err := json.Unmarshal([]byte(equalB), &decodedNonMember); err != nil {
		t.Fatal(err)
	}
	if err := acc.VerifyNonMembership(decodedNonMember); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("b == x JSON error = %v, want ErrInvalidRange", err)
	}
	trailing := string(encoded) + "{}"
	if err := json.Unmarshal([]byte(trailing), &decodedNonMember); err == nil {
		t.Fatal("JSON with trailing data was accepted")
	}
	nonPrimeJSON := fmt.Sprintf(`{"set_id":"json","generation":2,"root_digest":%q,"x":9,"w":%q}`, digest, member.W.String())
	if err := json.Unmarshal([]byte(nonPrimeJSON), &decodedMember); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("non-prime JSON x error = %v, want ErrInvalidElement", err)
	}
}

func TestIsPrime32(t *testing.T) {
	primes := []uint32{2, 3, 5, 7, 65537, 4294967291}
	for _, p := range primes {
		if !IsPrime32(p) {
			t.Fatalf("%d reported composite", p)
		}
	}
	composites := []uint32{0, 1, 4, 9, 25, 3215031751, 4294967295}
	for _, c := range composites {
		if IsPrime32(c) {
			t.Fatalf("%d reported prime", c)
		}
	}
}

func TestSameRootDifferentSetIDWithFreeVerifier(t *testing.T) {
	acc, err := New(refN, refG, "a", 3, []uint32{3, 5})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := acc.ProveMembership(3)
	if err != nil {
		t.Fatal(err)
	}
	wrongIdentity, err := HostIdentity(refN, refG, acc.Root(), "b", acc.Generation())
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyMembershipProof(refN, refG, acc.Root(), wrongIdentity, proof)
	if !errors.Is(err, ErrSetIDMismatch) {
		t.Fatalf("wrong set ID error = %v, want ErrSetIDMismatch", err)
	}
	if err := VerifyMembershipAt(refN, refG, acc.Root(), "b", acc.Generation(), proof); !errors.Is(err, ErrSetIDMismatch) {
		t.Fatalf("VerifyMembershipAt wrong ID error = %v", err)
	}
}

func TestAllPrimesUpToBoundAreClassified(t *testing.T) {
	// A wider reference check guards against the Miller-Rabin loop accidentally
	// treating the a^d == -1 case as composite.
	for n := uint32(2); n < 5000; n++ {
		want := true
		for d := uint32(2); d*d <= n; d++ {
			if n%d == 0 {
				want = false
				break
			}
		}
		if got := IsPrime32(n); got != want {
			t.Fatalf("IsPrime32(%d) = %v, want %v", n, got, want)
		}
	}
}
