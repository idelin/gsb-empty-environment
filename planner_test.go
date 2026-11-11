package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func ratEq(a, b *big.Rat) bool { return a.Cmp(b) == 0 }

// bayes is an independent reference implementation of the Bayesian update
// used only by the tests. Given normalized belief b, probe p and response r,
// it returns the response probability under b and the normalized posterior.
func bayes(b []*big.Rat, probes []Probe, p, r int) (*big.Rat, []*big.Rat) {
	n := len(b)
	weighted := make([]*big.Rat, n)
	total := new(big.Rat)
	for s := 0; s < n; s++ {
		weighted[s] = new(big.Rat).Mul(b[s], probes[p].Responses[r][s])
		total.Add(total, weighted[s])
	}
	post := make([]*big.Rat, n)
	if total.Sign() == 0 {
		for s := range post {
			post[s] = new(big.Rat)
		}
		return total, post
	}
	for s := 0; s < n; s++ {
		post[s] = new(big.Rat).Quo(weighted[s], total)
	}
	return total, post
}

// TestPosteriorUpdate checks an explicit one-round binary symmetric channel
// against hand-computed posteriors and success probability.
func TestPosteriorUpdate(t *testing.T) {
	prob := Problem{
		Priors: []*big.Rat{rat(1, 2), rat(1, 2)},
		Probes: []Probe{{
			Name: "bsc",
			Responses: [][]*big.Rat{
				{rat(3, 4), rat(1, 4)},
				{rat(1, 4), rat(3, 4)},
			},
		}},
		Rounds: 1,
	}

	res, err := Solve(prob)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !ratEq(res.MaxSuccessProbability, rat(3, 4)) {
		t.Fatalf("max success = %s, want 3/4", res.MaxSuccessProbability.RatString())
	}

	root := res.Root
	if root.Probe != 0 || len(root.Children) != 2 {
		t.Fatalf("root = probe %d with %d children, want probe 0 with 2 children", root.Probe, len(root.Children))
	}

	for r := 0; r < 2; r++ {
		total, post := bayes(prob.Priors, prob.Probes, 0, r)
		if !ratEq(root.ChildProbability[r], total) {
			t.Fatalf("branch %d prob = %s, want %s", r, root.ChildProbability[r].RatString(), total.RatString())
		}
		child := root.Children[r]
		for s := range post {
			if !ratEq(child.Belief[s], post[s]) {
				t.Fatalf("branch %d posterior[%d] = %s, want %s", r, s, child.Belief[s].RatString(), post[s].RatString())
			}
		}
		if child.Probe != -1 || child.Guess != r {
			t.Fatalf("branch %d leaf guess = %d, want %d", r, child.Guess, r)
		}
	}
}

// TestZeroProbabilityBranch covers both a response that only one secret can
// produce and a response impossible under the current belief (skipped branch).
func TestZeroProbabilityBranch(t *testing.T) {
	prob := Problem{
		Priors: []*big.Rat{rat(9, 10), rat(1, 10)},
		Probes: []Probe{{
			Name: "leak",
			Responses: [][]*big.Rat{
				{rat(1, 1), rat(1, 2)},
				{rat(0, 1), rat(1, 2)},
			},
		}},
		Rounds: 1,
	}

	res, err := Solve(prob)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// r0: total 19/20, posterior (18/19, 1/19) -> guess 0.
	// r1: total 1/20, posterior (0, 1)        -> guess 1.
	if !ratEq(res.MaxSuccessProbability, rat(19, 20)) {
		t.Fatalf("max success = %s, want 19/20", res.MaxSuccessProbability.RatString())
	}
	root := res.Root
	if root.Probe != 0 || len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(root.Children))
	}
	if !ratEq(root.Children[1].Belief[0], rat(0, 1)) {
		t.Fatalf("certain branch posterior[0] = %s, want 0", root.Children[1].Belief[0].RatString())
	}
	if root.Children[1].Guess != 1 {
		t.Fatalf("certain branch guess = %d, want 1", root.Children[1].Guess)
	}

	// A response with total probability zero under the belief must be pruned.
	impossible := Problem{
		Priors: []*big.Rat{rat(1, 1), rat(0, 1)},
		Probes: []Probe{{
			Name: "dead-response",
			Responses: [][]*big.Rat{
				{rat(1, 1), rat(1, 4)},
				{rat(0, 1), rat(3, 4)},
			},
		}},
		Rounds: 1,
	}
	res2, err := Solve(impossible)
	if err != nil {
		t.Fatalf("Solve impossible: %v", err)
	}
	if !ratEq(res2.MaxSuccessProbability, rat(1, 1)) {
		t.Fatalf("impossible-case max = %s, want 1", res2.MaxSuccessProbability.RatString())
	}
	// Secret 0 is already certain: probing cannot help, so the optimal policy
	// stops immediately instead of traversing the dead response branch.
	if res2.Root.Probe != -1 {
		t.Fatalf("certain prior should stop immediately, got probe node %d", res2.Root.Probe)
	}
	if res2.Root.Guess != 0 {
		t.Fatalf("certain prior guess = %d, want 0", res2.Root.Guess)
	}
}

// TestRepeatedProbe shows that re-issuing the same probe accumulates evidence
// (conditional independence given the secret) and strictly improves success.
func TestRepeatedProbe(t *testing.T) {
	probe := Probe{
		Name: "ternary",
		Responses: [][]*big.Rat{
			{rat(1, 2), rat(1, 4)},
			{rat(1, 4), rat(1, 2)},
			{rat(1, 4), rat(1, 4)},
		},
	}

	one, err := Solve(Problem{Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Probes: []Probe{probe}, Rounds: 1})
	if err != nil {
		t.Fatalf("Solve round 1: %v", err)
	}
	two, err := Solve(Problem{Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Probes: []Probe{probe}, Rounds: 2})
	if err != nil {
		t.Fatalf("Solve round 2: %v", err)
	}

	// Hand-computed values.
	if !ratEq(one.MaxSuccessProbability, rat(5, 8)) {
		t.Fatalf("one round = %s, want 5/8", one.MaxSuccessProbability.RatString())
	}
	// Round 2 aggregates two conditionally independent draws: each secret's
	// best response-pair mass is 3/8 (two pairs out of four), giving
	// 1/2 * 3/8 + 1/2 * 3/8 = 21/32 once singleton pairs are included exactly.
	if !ratEq(two.MaxSuccessProbability, rat(21, 32)) {
		t.Fatalf("two rounds = %s, want 21/32", two.MaxSuccessProbability.RatString())
	}
	if two.MaxSuccessProbability.Cmp(one.MaxSuccessProbability) <= 0 {
		t.Fatalf("repeating the probe did not help: %s <= %s", two.MaxSuccessProbability.RatString(), one.MaxSuccessProbability.RatString())
	}

	// Both levels of the tree must reuse probe 0.
	root := two.Root
	if root.Probe != 0 {
		t.Fatalf("root probe = %d, want 0", root.Probe)
	}
	for i, child := range root.Children {
		if child.Depth != 1 {
			t.Fatalf("child %d depth = %d, want 1", i, child.Depth)
		}
	}
	// The informative posteriors (responses 0 and 1) cannot be improved by
	// another draw, so those branches stop; only the neutral response 2
	// benefits from re-issuing the same probe.
	if two.Root.Children[0].Probe != -1 || two.Root.Children[1].Probe != -1 {
		t.Fatalf("informative branches should stop early")
	}
	neutral := two.Root.Children[2]
	if neutral.Probe != 0 {
		t.Fatalf("neutral branch probe = %d, want repeated probe 0", neutral.Probe)
	}
	for _, grandchild := range neutral.Children {
		if grandchild.Depth != 2 || grandchild.Probe != -1 {
			t.Fatalf("second-round node = depth %d probe %d, want depth-2 leaf", grandchild.Depth, grandchild.Probe)
		}
	}
}

// --- Full strategy enumeration reference solver -----------------------------

type bfNode struct {
	probe    int
	guess    int
	children []*bfNode
}

// enumerateBFTrees builds every complete policy tree for a problem whose
// probes all share nResp responses. Internal nodes may also stop (guessing
// any secret), which mirrors the planner's early-stopping option.
func enumerateBFTrees(nSecrets, nProbes, nResp, roundsLeft int) []*bfNode {
	leaves := make([]*bfNode, 0, nSecrets)
	for g := 0; g < nSecrets; g++ {
		leaves = append(leaves, &bfNode{probe: -1, guess: g})
	}
	if roundsLeft == 0 {
		return leaves
	}

	smaller := enumerateBFTrees(nSecrets, nProbes, nResp, roundsLeft-1)
	trees := append([]*bfNode{}, leaves...)
	for p := 0; p < nProbes; p++ {
		combos := [][]*bfNode{{}}
		for r := 0; r < nResp; r++ {
			next := make([][]*bfNode, 0, len(combos)*len(smaller))
			for _, prefix := range combos {
				for _, sub := range smaller {
					merged := append(append([]*bfNode{}, prefix...), sub)
					next = append(next, merged)
				}
			}
			combos = next
		}
		for _, children := range combos {
			trees = append(trees, &bfNode{probe: p, children: children})
		}
	}
	return trees
}

// evalBF evaluates one enumerated policy under exact rational arithmetic.
func evalBF(node *bfNode, b []*big.Rat, probes []Probe) *big.Rat {
	if node.probe == -1 {
		return new(big.Rat).Set(b[node.guess])
	}
	total := new(big.Rat)
	for r := range node.children {
		branchProb, post := bayes(b, probes, node.probe, r)
		if branchProb.Sign() == 0 {
			continue // unreachable branch contributes nothing
		}
		total.Add(total, new(big.Rat).Mul(branchProb, evalBF(node.children[r], post, probes)))
	}
	return total
}

// evalTree evaluates the planner's own strategy tree independently.
func evalTree(node *Node, probes []Probe) *big.Rat {
	if node.Probe == -1 {
		return new(big.Rat).Set(node.Belief[node.Guess])
	}
	total := new(big.Rat)
	for r, child := range node.Children {
		total.Add(total, new(big.Rat).Mul(node.ChildProbability[r], evalTree(child, probes)))
	}
	return total
}

func checkTreeInvariants(t *testing.T, node *Node, rounds, depth int) {
	t.Helper()
	if node.Depth != depth {
		t.Fatalf("node depth = %d, want %d", node.Depth, depth)
	}
	if depth > rounds {
		t.Fatalf("tree exceeds round budget: depth %d > %d", depth, rounds)
	}
	beliefSum := new(big.Rat)
	for _, pr := range node.Belief {
		if pr.Sign() < 0 {
			t.Fatalf("negative belief entry %s", pr.RatString())
		}
		beliefSum.Add(beliefSum, pr)
	}
	if !ratEq(beliefSum, rat(1, 1)) {
		t.Fatalf("belief sums to %s, want 1", beliefSum.RatString())
	}
	if node.Probe == -1 {
		if len(node.Children) != 0 || node.Guess < 0 || node.Guess >= len(node.Belief) {
			t.Fatalf("malformed leaf: %+v", node)
		}
		return
	}
	branchSum := new(big.Rat)
	for _, pr := range node.ChildProbability {
		if pr.Sign() <= 0 {
			t.Fatalf("non-positive child probability %s", pr.RatString())
		}
		branchSum.Add(branchSum, pr)
	}
	if !ratEq(branchSum, rat(1, 1)) {
		t.Fatalf("child probabilities sum to %s, want 1", branchSum.RatString())
	}
	if len(node.Children) != len(node.ChildProbability) {
		t.Fatalf("children/probability length mismatch")
	}
	for _, child := range node.Children {
		checkTreeInvariants(t, child, rounds, depth+1)
	}
}

func randProblem(rng *rand.Rand, nSecrets, nProbes, nResp int) Problem {
	priorWeights := make([]int64, nSecrets)
	priorSum := int64(0)
	for s := range priorWeights {
		priorWeights[s] = int64(rng.Intn(5)) // zero prior is allowed
		priorSum += priorWeights[s]
	}
	if priorSum == 0 {
		priorWeights[0] = 1
		priorSum = 1
	}
	priors := make([]*big.Rat, nSecrets)
	for s, w := range priorWeights {
		priors[s] = big.NewRat(w, priorSum)
	}

	probes := make([]Probe, nProbes)
	for p := range probes {
		rows := make([][]*big.Rat, nResp)
		for r := range rows {
			rows[r] = make([]*big.Rat, nSecrets)
		}
		for s := 0; s < nSecrets; s++ {
			weights := make([]int64, nResp)
			sum := int64(0)
			for r := range weights {
				weights[r] = int64(rng.Intn(4))
				sum += weights[r]
			}
			if sum == 0 {
				weights[0] = 1
				sum = 1
			}
			for r, w := range weights {
				rows[r][s] = big.NewRat(w, sum)
			}
		}
		probes[p] = Probe{Name: "p", Responses: rows}
	}

	return Problem{Priors: priors, Probes: probes, Rounds: 0}
}

// TestBruteForceCrossCheck enumerates every complete adaptive policy on small
// instances and requires the memoized planner to match the best enumerated
// policy exactly.
func TestBruteForceCrossCheck(t *testing.T) {
	type shape struct{ nSecrets, nProbes, nResp, rounds int }
	shapes := []shape{
		{2, 1, 2, 2},
		{2, 2, 2, 2},
		{2, 1, 3, 1},
		{2, 2, 3, 1},
		{3, 1, 2, 2},
		{3, 2, 2, 1},
	}

	rng := rand.New(rand.NewSource(20261111))
	for _, sh := range shapes {
		for trial := 0; trial < 4; trial++ {
			prob := randProblem(rng, sh.nSecrets, sh.nProbes, sh.nResp)
			prob.Rounds = sh.rounds

			trees := enumerateBFTrees(sh.nSecrets, sh.nProbes, sh.nResp, sh.rounds)
			best := new(big.Rat)
			for _, tree := range trees {
				v := evalBF(tree, prob.Priors, prob.Probes)
				if v.Cmp(best) > 0 {
					best.Set(v)
				}
			}

			res, err := Solve(prob)
			if err != nil {
				t.Fatalf("shape %+v trial %d: Solve: %v", sh, trial, err)
			}
			if !ratEq(res.MaxSuccessProbability, best) {
				t.Fatalf("shape %+v trial %d: planner %s != brute force %s (%d policies)",
					sh, trial, res.MaxSuccessProbability.RatString(), best.RatString(), len(trees))
			}

			// The emitted tree must itself attain the reported optimum.
			treeValue := evalTree(res.Root, prob.Probes)
			if !ratEq(treeValue, best) {
				t.Fatalf("shape %+v trial %d: tree value %s != optimum %s",
					sh, trial, treeValue.RatString(), best.RatString())
			}
			checkTreeInvariants(t, res.Root, sh.rounds, 0)
		}
	}
}

func TestValidation(t *testing.T) {
	bad := []Problem{
		{Priors: nil, Rounds: 1},
		{Priors: make([]*big.Rat, MaxSecrets+1), Rounds: 1},
		{Priors: []*big.Rat{nil, rat(1, 1)}, Rounds: 1},
		{Priors: []*big.Rat{rat(1, 3), rat(1, 3)}, Rounds: 1},
		{Priors: []*big.Rat{rat(-1, 2), rat(3, 2)}, Rounds: 1},
		{Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Rounds: MaxRounds + 1},
		{Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Rounds: 1,
			Probes: []Probe{{}, {}, {}, {}, {}, {}}},
		{
			Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Rounds: 1,
			Probes: []Probe{{Responses: [][]*big.Rat{
				{rat(1, 2)}, {rat(1, 2)}, {rat(1, 2)}, {rat(1, 2)},
			}}},
		},
		{
			Priors: []*big.Rat{rat(1, 2), rat(1, 2)}, Rounds: 1,
			Probes: []Probe{{Responses: [][]*big.Rat{
				{rat(1, 2), rat(1, 2)},
				{rat(1, 4), rat(1, 4)}, // columns sum to 3/4
			}}},
		},
	}

	for i, p := range bad {
		if _, err := Solve(p); err == nil {
			t.Fatalf("bad case %d: expected error, got nil", i)
		}
	}
}

func TestNoProbesAndZeroRounds(t *testing.T) {
	priors := []*big.Rat{rat(1, 4), rat(1, 2), rat(1, 4)}

	res, err := Solve(Problem{Priors: priors, Rounds: 3})
	if err != nil {
		t.Fatalf("Solve with no probes: %v", err)
	}
	if !ratEq(res.MaxSuccessProbability, rat(1, 2)) {
		t.Fatalf("no-probe value = %s, want 1/2", res.MaxSuccessProbability.RatString())
	}
	if res.Root.Probe != -1 || res.Root.Guess != 1 {
		t.Fatalf("no-probe root = %+v, want leaf guessing 1", res.Root)
	}

	res0, err := Solve(Problem{Priors: priors, Rounds: 0, Probes: []Probe{{
		Responses: [][]*big.Rat{
			{rat(1, 1), rat(1, 1), rat(1, 1)},
		},
	}}})
	if err != nil {
		t.Fatalf("Solve zero rounds: %v", err)
	}
	if !ratEq(res0.MaxSuccessProbability, rat(1, 2)) || res0.Root.Probe != -1 {
		t.Fatalf("zero-round result = %+v, want immediate guess", res0)
	}
}

// TestMaxScaleSmoke exercises the full limits (8 secrets, 5 probes, 3
// responses, 5 rounds) with deterministic probes so the belief graph stays
// small while the recursion and tree construction are fully exercised.
func TestMaxScaleSmoke(t *testing.T) {
	const nSecrets, nProbes, nResp, rounds = 8, 5, 3, 5
	priors := make([]*big.Rat, nSecrets)
	for s := range priors {
		priors[s] = big.NewRat(1, nSecrets)
	}
	probes := make([]Probe, nProbes)
	bitProbe := func(bit uint) Probe {
		rows := make([][]*big.Rat, nResp)
		for r := range rows {
			rows[r] = make([]*big.Rat, nSecrets)
		}
		for s := 0; s < nSecrets; s++ {
			value := (s >> bit) & 1
			rows[value][s] = rat(1, 1)
			rows[1-value][s] = rat(0, 1)
			rows[2][s] = rat(0, 1) // unused, globally impossible response
		}
		return Probe{Responses: rows}
	}
	// Three probes reveal the three bits of the secret index; the remaining
	// probes repeat earlier channels.
	probes[0] = bitProbe(0)
	probes[1] = bitProbe(1)
	probes[2] = bitProbe(2)
	probes[3] = bitProbe(0)
	probes[4] = bitProbe(1)

	res, err := Solve(Problem{Priors: priors, Probes: probes, Rounds: rounds})
	if err != nil {
		t.Fatalf("max-scale Solve: %v", err)
	}
	// The three bit-revealing probes distinguish every secret exactly.
	if !ratEq(res.MaxSuccessProbability, rat(1, 1)) {
		t.Fatalf("max-scale value = %s, want 1", res.MaxSuccessProbability.RatString())
	}
	checkTreeInvariants(t, res.Root, rounds, 0)
	if !ratEq(evalTree(res.Root, probes), rat(1, 1)) {
		t.Fatalf("max-scale tree does not attain 1")
	}
}
