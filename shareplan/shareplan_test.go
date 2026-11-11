package shareplan

import (
	"errors"
	mathrand "math/rand"
	"testing"
)

type byteRNG struct{ r *mathrand.Rand }

func (b byteRNG) Read(p []byte) (int, error) { return b.r.Read(p) }

func leaf(name string) *Node { return &Node{Participant: name} }

func threshold(t int, children ...*Node) *Node {
	return &Node{Threshold: t, Children: children}
}

func sharesForParticipants(t *testing.T, plan *Plan, participants []string) []*LeafShare {
	t.Helper()
	want := make(map[string]bool, len(participants))
	for _, name := range participants {
		want[name] = true
	}
	var out []*LeafShare
	for _, share := range plan.Leaves() {
		if want[share.Participant] {
			copy := *share
			out = append(out, &copy)
		}
	}
	return out
}

func TestSimpleThreshold(t *testing.T) {
	root := threshold(2, leaf("alice"), leaf("bob"), leaf("carol"))
	plan, err := Split(root, 42, nil)
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	if got := plan.Participants(); len(got) != 3 || got[0] != "alice" {
		t.Fatalf("unexpected participants: %v", got)
	}
	if len(plan.MinimalParticipantSets()) != 3 {
		t.Fatalf("expected 3 minimal pairs, got %v", plan.MinimalParticipantSets())
	}

	if _, err := plan.Recover(sharesForParticipants(t, plan, []string{"alice"})); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("single participant must not recover, got %v", err)
	}

	got, err := plan.Recover(sharesForParticipants(t, plan, []string{"bob", "carol"}))
	if err != nil || got != 42 {
		t.Fatalf("two participants should recover 42, got %d err=%v", got, err)
	}

	if err := plan.VerifyRecovery(42); err != nil {
		t.Fatalf("exhaustive verification: %v", err)
	}

	recon, err := plan.ReconstructionFor([]string{"alice", "carol"})
	if err != nil {
		t.Fatalf("reconstruction: %v", err)
	}
	linear, err := recon.Secret(sharesForParticipants(t, plan, []string{"alice", "carol"}))
	if err != nil || linear != 42 {
		t.Fatalf("linear reconstruction gave %d, %v", linear, err)
	}
}

func TestNestedPolicy(t *testing.T) {
	inner := threshold(2, leaf("bob"), leaf("carol"), leaf("dave"))
	root := threshold(2, leaf("alice"), inner, leaf("erin"))

	plan, err := Split(root, 200, byteRNG{mathrand.New(mathrand.NewSource(7))})
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	if !plan.Qualified([]string{"alice", "erin"}) {
		t.Fatal("alice+erin should satisfy root 2-of-3")
	}
	if plan.Qualified([]string{"alice", "bob"}) {
		t.Fatal("one inner share must not satisfy the inner threshold")
	}
	if plan.Qualified([]string{"bob", "carol"}) {
		t.Fatal("a satisfied inner child alone is only one of two required root children")
	}
	if !plan.Qualified([]string{"bob", "carol", "alice"}) {
		t.Fatal("inner group plus one sibling should satisfy root 2-of-3")
	}

	if _, err := plan.Recover(sharesForParticipants(t, plan, []string{"bob", "carol"})); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("one satisfied root child must not recover, got %v", err)
	}
	got, err := plan.Recover(sharesForParticipants(t, plan, []string{"bob", "carol", "alice"}))
	if err != nil || got != 200 {
		t.Fatalf("inner group plus sibling recovery gave %d, %v", got, err)
	}
	if _, err := plan.Recover(sharesForParticipants(t, plan, []string{"alice", "bob"})); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("partial inner group must fail, got %v", err)
	}

	if err := plan.VerifyRecovery(200); err != nil {
		t.Fatalf("exhaustive verification: %v", err)
	}

	for _, set := range plan.MinimalParticipantSets() {
		recon, err := plan.ReconstructionFor(set)
		if err != nil {
			t.Fatalf("coefficients for %v: %v", set, err)
		}
		linear, err := recon.Secret(sharesForParticipants(t, plan, set))
		if err != nil || linear != 200 {
			t.Fatalf("coefficients for %v gave %d, %v", set, linear, err)
		}
	}
}

func TestRepeatedParticipant(t *testing.T) {
	root := threshold(2,
		threshold(2, leaf("alice"), leaf("bob")),
		threshold(2, leaf("alice"), leaf("carol")),
	)
	plan, err := Split(root, 77, byteRNG{mathrand.New(mathrand.NewSource(11))})
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	aliceLeaves := 0
	ids := map[string]bool{}
	for _, share := range plan.Leaves() {
		if share.Participant != "alice" {
			continue
		}
		aliceLeaves++
		if ids[share.ShareID] {
			t.Fatal("repeated leaf got a duplicated share id")
		}
		ids[share.ShareID] = true
	}
	if aliceLeaves != 2 {
		t.Fatalf("expected two independent alice shares, got %d", aliceLeaves)
	}

	if !plan.Qualified([]string{"alice", "bob", "carol"}) {
		t.Fatal("alice plus both group partners should qualify")
	}
	if plan.Qualified([]string{"bob", "carol"}) {
		t.Fatal("without alice neither 2-of-2 child can satisfy")
	}

	got, err := plan.Recover(sharesForParticipants(t, plan, []string{"alice", "bob", "carol"}))
	if err != nil || got != 77 {
		t.Fatalf("repeated-participant recovery gave %d, %v", got, err)
	}

	recon, err := plan.ReconstructionFor([]string{"alice", "bob", "carol"})
	if err != nil {
		t.Fatalf("reconstruction: %v", err)
	}
	if recon.Coeff["alice"] == 0 {
		t.Fatal("alice must have a nonzero aggregated coefficient")
	}
	if err := plan.VerifyRecovery(77); err != nil {
		t.Fatalf("exhaustive verification: %v", err)
	}
}

func TestCombineRejectsCrossNodeAndBadShares(t *testing.T) {
	inner := threshold(2, leaf("bob"), leaf("carol"))
	root := threshold(2, leaf("alice"), inner, leaf("dave"))
	plan, err := Split(root, 5, byteRNG{mathrand.New(mathrand.NewSource(3))})
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	rootShares := plan.InternalShares("root")
	innerShares := plan.InternalShares("root/c1")

	mixed := []NodeShare{rootShares[0], innerShares[0]}
	if _, _, err := plan.Combine("root", mixed); !errors.Is(err, ErrMixedNodes) {
		t.Fatalf("mixing child shares from another node: got %v", err)
	}

	if _, _, err := plan.Combine("root", rootShares[:1]); !errors.Is(err, ErrNotEnoughShares) {
		t.Fatalf("below threshold: got %v", err)
	}

	if _, _, err := plan.Combine("root", []NodeShare{rootShares[0], rootShares[0]}); !errors.Is(err, ErrDuplicateShare) {
		t.Fatalf("duplicate share: got %v", err)
	}

	tampered := []NodeShare{rootShares[0], rootShares[1]}
	tampered[1].X = rootShares[0].X
	if _, _, err := plan.Combine("root", tampered); !errors.Is(err, ErrDuplicateX) {
		t.Fatalf("duplicate abscissa: got %v", err)
	}

	foreignValue := []NodeShare{rootShares[0], rootShares[1]}
	foreignValue[1].Y = P
	if _, _, err := plan.Combine("root", foreignValue); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("out-of-field value: got %v", err)
	}

	secret, lagrange, err := plan.Combine("root", rootShares[:2])
	if err != nil || secret != 5 {
		t.Fatalf("same-node combine gave %d, %v", secret, err)
	}
	if len(lagrange) != 2 {
		t.Fatalf("expected 2 lagrange coefficients, got %d", len(lagrange))
	}

	if _, _, err := plan.Combine("root/c0", innerShares); !errors.Is(err, ErrNodeNotInternal) {
		t.Fatalf("combine on leaf: got %v", err)
	}
}

func TestRecoverRejectsBadLeafShares(t *testing.T) {
	root := threshold(2, leaf("alice"), leaf("bob"), leaf("carol"))
	plan, err := Split(root, 9, nil)
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	leaves := plan.Leaves()

	unknown := *leaves[0]
	unknown.ShareID = "leaf-99"
	if _, err := plan.Recover([]*LeafShare{&unknown, leaves[1]}); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("unknown share: got %v", err)
	}

	wrongNode := *leaves[0]
	wrongNode.NodeID = "root/c1"
	if _, err := plan.Recover([]*LeafShare{&wrongNode, leaves[1]}); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("share bound to another node: got %v", err)
	}

	if _, err := plan.Recover([]*LeafShare{leaves[0], leaves[0]}); !errors.Is(err, ErrDuplicateShare) {
		t.Fatalf("duplicate leaf share: got %v", err)
	}

	badValue := *leaves[0]
	badValue.Y = P
	if _, err := plan.Recover([]*LeafShare{&badValue, leaves[1]}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("out-of-field leaf value: got %v", err)
	}
}

func TestRecoverRejectsShareRelocatedToAnotherNode(t *testing.T) {
	root := threshold(2,
		threshold(2, leaf("alice"), leaf("bob")),
		threshold(2, leaf("alice"), leaf("carol")),
	)
	plan, err := Split(root, 33, byteRNG{mathrand.New(mathrand.NewSource(5))})
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	var aliceShares []*LeafShare
	for _, share := range plan.Leaves() {
		if share.Participant == "alice" {
			aliceShares = append(aliceShares, share)
		}
	}
	if len(aliceShares) != 2 {
		t.Fatalf("expected two alice occurrences, got %d", len(aliceShares))
	}

	// Keep a valid ShareID but claim the share belongs to the other alice
	// leaf node: recovery must reject the relocated share instead of
	// interpolating it at the wrong node.
	relocated := *aliceShares[0]
	relocated.NodeID = aliceShares[1].NodeID
	presented := []*LeafShare{&relocated, aliceShares[1]}
	if _, err := plan.Recover(presented); !errors.Is(err, ErrUnknownShare) {
		t.Fatalf("share relocated across nodes must be rejected, got %v", err)
	}

	// Presenting each independently identified alice share at its own node
	// together with both partners succeeds.
	good := sharesForParticipants(t, plan, []string{"alice", "bob", "carol"})
	got, err := plan.Recover(good)
	if err != nil || got != 33 {
		t.Fatalf("legit repeated-participant recovery gave %d, %v", got, err)
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		root *Node
		want error
	}{
		{"nil", nil, ErrEmptyPolicy},
		{"empty participant", threshold(1, leaf("")), ErrMalformedNode},
		{"internal with participant", &Node{Threshold: 1, Participant: "x", Children: []*Node{leaf("a")}}, ErrMalformedNode},
		{"threshold zero", threshold(0, leaf("a"), leaf("b")), ErrInvalidThreshold},
		{"threshold too large", threshold(3, leaf("a"), leaf("b")), ErrInvalidThreshold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Split(tc.root, 1, nil); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}

	var children []*Node
	for i := 0; i <= MaxParticipants; i++ {
		children = append(children, leaf("p"+string(rune('a'+i))))
	}
	if _, err := Split(threshold(1, children...), 1, nil); !errors.Is(err, ErrTooManyParticipants) {
		t.Fatalf("participant cap: got %v", err)
	}

	if _, err := Split(threshold(1, leaf("a")), P, nil); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("secret range: got %v", err)
	}
}

func TestExhaustiveRandomPolicies(t *testing.T) {
	policyRNG := mathrand.New(mathrand.NewSource(4242))
	for iter := 0; iter < 40; iter++ {
		root, names := randomPolicy(policyRNG, 1+policyRNG.Intn(MaxParticipants))
		secret := policyRNG.Intn(P)

		splitRNG := mathrand.New(mathrand.NewSource(int64(9000 + iter)))
		plan, err := Split(root, secret, byteRNG{splitRNG})
		if err != nil {
			t.Fatalf("iter %d split: %v", iter, err)
		}
		if len(plan.Participants()) != len(names) {
			t.Fatalf("iter %d participant mismatch: %v vs %v", iter, plan.Participants(), names)
		}
		if err := plan.VerifyRecovery(secret); err != nil {
			t.Fatalf("iter %d verification: %v", iter, err)
		}
	}
}

// randomPolicy builds a random nested tree whose leaves use exactly
// participantCount distinct participants, allowing repeated occurrences.
func randomPolicy(r *mathrand.Rand, participantCount int) (*Node, []string) {
	names := make([]string, participantCount)
	for i := range names {
		names[i] = "p" + string(rune('a'+i))
	}

	var build func(depth int) *Node
	build = func(depth int) *Node {
		if depth >= 3 || r.Intn(3) == 0 {
			return leaf(names[r.Intn(len(names))])
		}
		n := 2 + r.Intn(3)
		children := make([]*Node, n)
		for i := range children {
			children[i] = build(depth + 1)
		}
		return threshold(1+r.Intn(n), children...)
	}

	root := build(0)
	idx, err := Validate(root)
	if err != nil {
		return randomPolicy(r, participantCount)
	}
	seen := map[string]bool{}
	for _, name := range idx.participants {
		seen[name] = true
	}
	if len(seen) != participantCount {
		return randomPolicy(r, participantCount)
	}
	return root, names
}

func TestFieldArithmetic(t *testing.T) {
	for v := 1; v < P; v++ {
		if mul(v, inverse(v)) != 1 {
			t.Fatalf("inverse failed for %d", v)
		}
	}

	// Known 2-of-3 split: f(x) = 5 + 2x over GF(257).
	coeffs := []int{5, 2}
	want := []int{7, 9, 11}
	for i, expected := range want {
		if got := evalPoly(coeffs, i+1); got != expected {
			t.Fatalf("eval at x=%d: want %d got %d", i+1, expected, got)
		}
	}

	secret, lagrange := interpolateAtZero([]point{{x: 1, y: 7}, {x: 2, y: 9}})
	if secret != 5 {
		t.Fatalf("interpolation gave %d", secret)
	}
	if got := add(mul(lagrange[0], 7), mul(lagrange[1], 9)); got != 5 {
		t.Fatalf("lagrange coefficients inconsistent: %d", got)
	}
}

func TestSingleLeafAndMaxFieldElement(t *testing.T) {
	plan, err := Split(leaf("sole"), P-1, nil)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	leaves := plan.Leaves()
	if len(leaves) != 1 || leaves[0].Y != P-1 {
		t.Fatalf("single leaf must hold the secret directly, got %+v", leaves)
	}

	got, err := plan.Recover(leaves)
	if err != nil || got != P-1 {
		t.Fatalf("single leaf recovery gave %d, %v", got, err)
	}

	minimal := plan.MinimalParticipantSets()
	if len(minimal) != 1 || len(minimal[0]) != 1 || minimal[0][0] != "sole" {
		t.Fatalf("unexpected minimal sets: %v", minimal)
	}
	if err := plan.VerifyRecovery(P - 1); err != nil {
		t.Fatalf("verification: %v", err)
	}
}

func TestSixteenParticipantExhaustive(t *testing.T) {
	children := make([]*Node, MaxParticipants)
	for i := range children {
		children[i] = leaf("p" + string(rune('a'+i)))
	}
	root := threshold(8, children...)

	plan, err := Split(root, 123, byteRNG{mathrand.New(mathrand.NewSource(99))})
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(plan.Participants()) != MaxParticipants {
		t.Fatalf("want %d participants", MaxParticipants)
	}
	if err := plan.VerifyRecovery(123); err != nil {
		t.Fatalf("16-participant exhaustive verification: %v", err)
	}

	minimal := plan.MinimalParticipantSets()
	// Every 8-subset of 16 participants is minimal: C(16,8) = 12870.
	if len(minimal) != 12870 {
		t.Fatalf("expected 12870 minimal 8-subsets, got %d", len(minimal))
	}
}
