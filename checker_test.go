package netsec

import (
	"errors"
	"testing"
)

func testPair(secret, candidate [SecretSize]byte) Pair {
	return Pair{Secret: secret, Candidate: candidate}
}

func TestCheckerReportsEarliestDivergenceAndFullWitness(t *testing.T) {
	// This program branches on secret[0] and then secret[1]. Different inputs
	// therefore diverge at the first branch or at the later addressed read.
	program := Program{
		Instructions: []Instruction{
			ReadAt(0, BaseSecret, 0),
			BranchIfZero(0, 3),
			ReadAt(1, BaseCandidate, 0),
			ReadAt(2, BaseSecret, 1),
			BranchIfZero(2, 6),
			ReadAt(3, BaseCandidate, 1),
		},
	}

	var aSecret, aCandidate [SecretSize]byte
	bSecret := aSecret
	bSecret[0] = 1
	cSecret := aSecret
	cSecret[1] = 1
	pairs := []Pair{
		testPair(aSecret, aCandidate),
		testPair(bSecret, aCandidate),
		testPair(cSecret, aCandidate),
	}

	report := CheckFinite(program, pairs, 64)
	if report.Status != StatusDivergent {
		t.Fatalf("status = %s, reason = %s", report.Status, report.Reason)
	}
	if report.ShortestDifference != 1 {
		t.Fatalf("shortest difference = %d, want 1", report.ShortestDifference)
	}
	if report.Witness == nil {
		t.Fatal("missing witness")
	}
	if report.Witness.LeftIndex != 0 || report.Witness.RightIndex != 1 {
		t.Fatalf("witness pair = (%d,%d), want (0,1)", report.Witness.LeftIndex, report.Witness.RightIndex)
	}
	if report.Witness.Left != pairs[0] || report.Witness.Right != pairs[1] {
		t.Fatal("witness did not contain both complete input pairs")
	}
	if !report.Witness.Difference.Left.Taken {
		t.Fatal("first witness branch should be taken for all-zero secret")
	}
	if report.Witness.Difference.Right.Taken {
		t.Fatal("second witness branch should not be taken for secret[0] != 0")
	}
	if report.Passed() || report.IsProof() {
		t.Fatal("divergent report passed or claimed proof")
	}
}

func TestWitnessPreservesAll32Bytes(t *testing.T) {
	program := Program{
		Instructions: []Instruction{
			ReadAt(0, BaseSecret, SecretSize-1),
			BranchIfZero(0, 3),
			Inc(1, 1),
		},
	}

	var left, right Pair
	for i := 0; i < SecretSize; i++ {
		left.Secret[i] = byte(0xa0 + i)
		left.Candidate[i] = byte(0x30 + i)
		right.Secret[i] = left.Secret[i]
		right.Candidate[i] = left.Candidate[i]
	}
	right.Secret[SecretSize-1] = 0

	report := CheckFinite(program, []Pair{left, right}, 32)
	if report.Status != StatusDivergent || report.Witness == nil {
		t.Fatalf("status = %s, witness = %v", report.Status, report.Witness)
	}
	if report.Witness.Left != left || report.Witness.Right != right {
		t.Fatal("witness inputs differ from the supplied 32-byte values")
	}
	if report.Witness.Left.Secret[31] != left.Secret[31] || report.Witness.Right.Candidate[31] != right.Candidate[31] {
		t.Fatal("last witness byte was truncated or altered")
	}
}

func TestBranchFreeCompareIsConsistentButNotAProof(t *testing.T) {
	var secret, other [SecretSize]byte
	for i := 0; i < SecretSize; i++ {
		secret[i] = byte(i * 3)
		other[i] = secret[i]
	}
	other[17] ^= 0x80

	report := CheckFinite(CompareProgram(), []Pair{
		testPair(secret, secret),
		testPair(secret, other),
		testPair(other, other),
		testPair(other, secret),
	}, BuiltinCompareSteps)

	if report.Status != StatusConsistent {
		t.Fatalf("status = %s, reason = %s", report.Status, report.Reason)
	}
	if !report.Passed() {
		t.Fatal("consistent finite corpus did not pass finite-corpus checking")
	}
	if report.IsProof() {
		t.Fatal("finite corpus was incorrectly treated as a proof")
	}
	if report.Scope != FiniteCorpusScope {
		t.Fatalf("scope = %q", report.Scope)
	}
	if report.PairsCompared != 6 {
		t.Fatalf("pairs compared = %d, want 6", report.PairsCompared)
	}
	if report.Witness != nil || report.ShortestDifference != -1 {
		t.Fatalf("consistent report has witness=%v difference=%d", report.Witness, report.ShortestDifference)
	}
}

func TestCheckerFailsClosedOnUnknownOrExhaustedBudget(t *testing.T) {
	var a, b Pair
	b.Secret[0] = 1
	pairs := []Pair{a, b}

	unknown := CheckFinite(CompareProgram(), pairs, 0)
	if unknown.Status != StatusUnknown || unknown.Passed() {
		t.Fatalf("zero budget status = %s, passed = %t", unknown.Status, unknown.Passed())
	}
	if !errors.Is(unknown.Err, ErrUnknownBudget) {
		t.Fatalf("zero budget cause = %v, want ErrUnknownBudget", unknown.Err)
	}

	exhausted := CheckFinite(CompareProgram(), pairs, 10)
	if exhausted.Status != StatusUnknown || exhausted.Passed() {
		t.Fatalf("short budget status = %s, passed = %t, reason = %s", exhausted.Status, exhausted.Passed(), exhausted.Reason)
	}
	if !errors.Is(exhausted.Err, ErrBudgetExceeded) {
		t.Fatalf("short budget cause = %v, want ErrBudgetExceeded", exhausted.Err)
	}

	infinite := Program{Instructions: []Instruction{
		Inc(0, 0),
		BranchIfNonZero(0, 0),
	}}
	loopReport := CheckFinite(infinite, pairs, 20)
	if loopReport.Status != StatusUnknown || loopReport.Passed() {
		t.Fatalf("infinite branch status = %s, passed = %t", loopReport.Status, loopReport.Passed())
	}
}

func TestCheckerRejectsTooManyInstructions(t *testing.T) {
	instructions := make([]Instruction, MaxInstructions+1)
	for i := range instructions {
		instructions[i] = Inc(0, 0)
	}
	program := Program{Instructions: instructions}
	report := CheckFinite(program, []Pair{{}, {}}, 1000)
	if report.Status != StatusInvalid || report.Passed() {
		t.Fatalf("status = %s, passed = %t", report.Status, report.Passed())
	}

	program.Instructions = instructions[:MaxInstructions]
	if err := program.Validate(); err != nil {
		t.Fatalf("128-instruction program rejected: %v", err)
	}
}

func TestCheckFiniteSets(t *testing.T) {
	var secret, candidate [SecretSize]byte
	for i := 0; i < SecretSize; i++ {
		secret[i] = byte(i)
		candidate[i] = byte(i)
	}
	different := candidate
	different[0] ^= 1

	report := CheckFiniteSets(CompareProgram(), [][]byte{secret[:]}, [][]byte{candidate[:], different[:]}, BuiltinCompareSteps)
	if report.Status != StatusConsistent || report.InputCount != 2 {
		t.Fatalf("status = %s, input count = %d, reason = %s", report.Status, report.InputCount, report.Reason)
	}

	report = CheckFiniteSets(CompareProgram(), [][]byte{secret[:]}, [][]byte{candidate[:]}, BuiltinCompareSteps)
	if report.Status != StatusUnknown || report.Passed() {
		t.Fatalf("single Cartesian product status = %s", report.Status)
	}

	short := candidate[:SecretSize-1]
	report = CheckFiniteSets(CompareProgram(), [][]byte{secret[:]}, [][]byte{candidate[:], short}, BuiltinCompareSteps)
	if report.Status != StatusInvalid || report.Passed() {
		t.Fatalf("short candidate status = %s", report.Status)
	}
}

func TestDiffTracesDetectsLengthDifference(t *testing.T) {
	left := Trace{{Kind: EventRead, Address: SecretBase}}
	right := Trace{}
	diff, ok := DiffTraces(left, right)
	if !ok {
		t.Fatal("different trace lengths not detected")
	}
	if diff.Step != 0 || !diff.RightEnded || diff.LeftEnded {
		t.Fatalf("unexpected difference: %+v", diff)
	}
}
