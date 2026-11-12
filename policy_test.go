package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

const (
	testCCPCR CommandCode = 0x0000017F
	testCCOR  CommandCode = 0x00000171
	testCCNV  CommandCode = 0x00000149
	testCCOth CommandCode = 0x00000199
)

var testCommands = CommandCodeTable{
	"pcr":     testCCPCR,
	"or":      testCCOR,
	"nv":      testCCNV,
	"foreign": testCCOth,
}

func extendForTest(t *testing.T, old []byte, commandCode CommandCode, params ...[]byte) []byte {
	t.Helper()
	if len(old) != DigestSize {
		t.Fatalf("old digest length = %d", len(old))
	}
	h := sha256.New()
	_, _ = h.Write(old)
	var code [4]byte
	binary.BigEndian.PutUint32(code[:], uint32(commandCode))
	_, _ = h.Write(code[:])
	for _, param := range params {
		_, _ = h.Write(param)
	}
	return h.Sum(nil)
}

func orForTest(t *testing.T, commandCode CommandCode, branches [][]byte) []byte {
	t.Helper()
	if len(branches) == 0 || len(branches) > MaxORBranches {
		t.Fatalf("invalid branch count %d", len(branches))
	}
	h := sha256.New()
	_, _ = h.Write(make([]byte, DigestSize))
	var code [4]byte
	binary.BigEndian.PutUint32(code[:], uint32(commandCode))
	_, _ = h.Write(code[:])
	for _, branch := range branches {
		if len(branch) != DigestSize {
			t.Fatalf("branch length = %d", len(branch))
		}
		_, _ = h.Write(branch)
	}
	return h.Sum(nil)
}

func TestEvaluateSingleORBranch(t *testing.T) {
	pcrA := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A"))
	pcrB := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B"))

	// The session walks only branch A. The trusted list still commits to both
	// branches, so the PolicyOR digest is bound to the full list.
	policy := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA, pcrB),
	)
	expected := orForTest(t, testCCOR, [][]byte{pcrA, pcrB})

	if err := Evaluate(policy, testCommands, expected); err != nil {
		t.Fatalf("valid single-branch traversal rejected: %v", err)
	}

	// A one-entry list is legal TPM PolicyOR input and must remain bound to
	// that single trusted branch.
	single := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA),
	)
	singleExpected := orForTest(t, testCCOR, [][]byte{pcrA})
	if err := Authorize(single, testCommands, singleExpected); err != nil {
		t.Fatalf("valid one-entry PolicyOR rejected: %v", err)
	}
}

func TestForgedORBranchListIsRejected(t *testing.T) {
	pcrA := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A"))
	pcrB := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B"))

	// This digest belongs to a different object/policy. Substituting it into
	// the OR list must not yield the protected object's authPolicy.
	foreign := extendForTest(t, ZeroDigest(), testCCOth, []byte("other-object"))
	original := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA, pcrB),
	)
	authPolicy := orForTest(t, testCCOR, [][]byte{pcrA, pcrB})

	forged := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA, foreign),
	)
	if err := Evaluate(forged, testCommands, authPolicy); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("forged branch list error = %v, want ErrPolicyMismatch", err)
	}
	if err := Evaluate(original, testCommands, authPolicy); err != nil {
		t.Fatalf("original policy rejected: %v", err)
	}

	// Dropping all but the branch the session happened to run is the other
	// form of OR-policy substitution. A one-entry list changes the committed
	// digest and must not be accepted.
	reduced := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA),
	)
	if err := Evaluate(reduced, testCommands, authPolicy); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("reduced branch list error = %v, want ErrPolicyMismatch", err)
	}
}

func TestORRejectsDigestOutsideTrustedBranches(t *testing.T) {
	pcrB := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B"))

	policy := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrB, extendForTest(t, ZeroDigest(), testCCOth, []byte("foreign"))),
	)
	_, err := EvaluateDigest(policy, testCommands)
	if !errors.Is(err, ErrORBranchNotMatched) {
		t.Fatalf("error = %v, want ErrORBranchNotMatched", err)
	}
}

func TestORRejectsMoreThanEightBranches(t *testing.T) {
	branches := make([][]byte, MaxORBranches+1)
	for i := range branches {
		branches[i] = extendForTest(t, ZeroDigest(), testCCPCR, []byte{byte(i)})
	}

	policy := NewPolicy(ORWithCommand("or", branches...))
	_, err := EvaluateDigest(policy, testCommands)
	if !errors.Is(err, ErrTooManyORBranches) {
		t.Fatalf("error = %v, want ErrTooManyORBranches", err)
	}
}

func TestORAcceptsExactlyEightBranches(t *testing.T) {
	branches := make([][]byte, MaxORBranches)
	for i := range branches {
		branches[i] = extendForTest(t, ZeroDigest(), testCCPCR, []byte{byte(i)})
	}

	policy := NewPolicy(
		Extend("pcr", []byte{0}),
		ORWithCommand("or", branches...),
	)
	expected := orForTest(t, testCCOR, branches)
	if err := Evaluate(policy, testCommands, expected); err != nil {
		t.Fatalf("eight-branch PolicyOR rejected: %v", err)
	}
}

func TestNestedPolicyOR(t *testing.T) {
	pcrA := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A"))
	pcrB := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B"))
	inner := orForTest(t, testCCOR, [][]byte{pcrA, pcrB})

	// A nested OR is a sequence: the inner PolicyOR digest becomes the current
	// digest that the outer OR must find in its trusted branch list.
	outerAlt := extendForTest(t, ZeroDigest(), testCCOth, []byte("outer-alt"))
	outer := orForTest(t, testCCOR, [][]byte{inner, outerAlt})

	policy := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrA, pcrB),
		ORWithCommand("or", inner, outerAlt),
	)
	if err := Evaluate(policy, testCommands, outer); err != nil {
		t.Fatalf("nested PolicyOR rejected: %v", err)
	}

	// Reversing the nesting order would require the PCR digest to be an outer
	// branch before the inner OR has run, which must be rejected.
	reversed := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", inner, outerAlt),
		ORWithCommand("or", pcrA, pcrB),
	)
	if _, err := EvaluateDigest(reversed, testCommands); !errors.Is(err, ErrORBranchNotMatched) {
		t.Fatalf("reversed nesting error = %v, want ErrORBranchNotMatched", err)
	}
}

func TestANDStepOrderIsCommitted(t *testing.T) {
	pcrArg := []byte("pcr0=A")
	nvArg := []byte("nv-counter=7")

	dPCR := extendForTest(t, ZeroDigest(), testCCPCR, pcrArg)
	expected := extendForTest(t, dPCR, testCCNV, nvArg)
	policy := NewPolicy(
		Extend("pcr", pcrArg),
		Extend("nv", nvArg),
	)
	if err := Evaluate(policy, testCommands, expected); err != nil {
		t.Fatalf("ordered AND policy rejected: %v", err)
	}

	swapped := NewPolicy(
		Extend("nv", nvArg),
		Extend("pcr", pcrArg),
	)
	if err := Evaluate(swapped, testCommands, expected); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("swapped AND steps error = %v, want ErrPolicyMismatch", err)
	}
}

func TestORBranchListOrderIsCommitted(t *testing.T) {
	pcrA := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A"))
	pcrB := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B"))
	authPolicy := orForTest(t, testCCOR, [][]byte{pcrA, pcrB})

	policy := NewPolicy(
		Extend("pcr", []byte("pcr0=A")),
		ORWithCommand("or", pcrB, pcrA),
	)
	digest, err := EvaluateDigest(policy, testCommands)
	if err != nil {
		t.Fatalf("swapped branch list failed to evaluate: %v", err)
	}
	if bytes.Equal(digest, authPolicy) {
		t.Fatal("PolicyOR digest ignored branch-list order")
	}
	if err := Evaluate(policy, testCommands, authPolicy); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("swapped branch list error = %v, want ErrPolicyMismatch", err)
	}
}

func TestEvaluationInputValidation(t *testing.T) {
	if err := Evaluate(NewPolicy(), testCommands, make([]byte, DigestSize-1)); !errors.Is(err, ErrInvalidAuthPolicy) {
		t.Fatalf("short authPolicy error = %v, want ErrInvalidAuthPolicy", err)
	}

	_, err := EvaluateDigest(NewPolicy(Extend("missing")), testCommands)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("missing command error = %v, want ErrUnknownCommand", err)
	}

	_, err = EvaluateDigest(NewPolicy(ORWithCommand("or", make([]byte, DigestSize-1))), testCommands)
	if !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("short branch error = %v, want ErrInvalidDigest", err)
	}

	_, err = EvaluateDigest(NewPolicy(ORWithCommand("or")), testCommands)
	if !errors.Is(err, ErrEmptyORBranchList) {
		t.Fatalf("empty branch list error = %v, want ErrEmptyORBranchList", err)
	}
}

func TestPrimitiveDigestFormulas(t *testing.T) {
	old := ZeroDigest()
	wantExtend := extendForTest(t, old, testCCPCR, []byte("pcr0=A"), []byte("extra"))
	gotExtend, err := ExtendDigest(old, testCCPCR, []byte("pcr0=A"), []byte("extra"))
	if err != nil {
		t.Fatalf("ExtendDigest: %v", err)
	}
	if !bytes.Equal(gotExtend, wantExtend) {
		t.Fatal("ExtendDigest did not match H(old || code || params)")
	}

	branches := [][]byte{
		extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A")),
		extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=B")),
	}
	wantOR := orForTest(t, testCCOR, branches)
	gotOR, err := PolicyORDigest(testCCOR, branches)
	if err != nil {
		t.Fatalf("PolicyORDigest: %v", err)
	}
	if !bytes.Equal(gotOR, wantOR) {
		t.Fatal("PolicyORDigest did not match H(0 || code || branches)")
	}
	if _, err := ExtendDigest(make([]byte, DigestSize-1), testCCPCR); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("short old digest error = %v, want ErrInvalidDigest", err)
	}
}

func TestEvaluatorAndUnknownStep(t *testing.T) {
	pcrA := extendForTest(t, ZeroDigest(), testCCPCR, []byte("pcr0=A"))
	policy := NewPolicy(Extend("pcr", []byte("pcr0=A")))
	evaluator := NewEvaluator(testCommands)

	digest, err := evaluator.Digest(policy)
	if err != nil {
		t.Fatalf("Evaluator.Digest: %v", err)
	}
	if !bytes.Equal(digest, pcrA) {
		t.Fatal("Evaluator.Digest returned wrong digest")
	}
	if err := evaluator.Authorize(policy, pcrA); err != nil {
		t.Fatalf("Evaluator.Authorize: %v", err)
	}

	_, err = EvaluateDigest(NewPolicy(Step{}), testCommands)
	if !errors.Is(err, ErrUnknownStepKind) {
		t.Fatalf("zero-value step error = %v, want ErrUnknownStepKind", err)
	}
}
