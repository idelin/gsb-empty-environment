package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const (
	// MaxSecrets is the largest number of hypotheses the planner accepts.
	MaxSecrets = 8
	// MaxProbes is the largest number of selectable probes.
	MaxProbes = 5
	// MaxResponses is the largest number of distinct responses per probe.
	MaxResponses = 3
	// MaxRounds is the largest permitted interaction horizon.
	MaxRounds = 5
)

var oneRat = big.NewRat(1, 1)

// Probe describes one available interaction channel. Responses[r][s] is the
// exact rational probability that the probe produces response r when the true
// secret is s. Each column (fixed s) must sum to exactly 1. Probes may use
// different response counts, up to MaxResponses entries.
type Probe struct {
	Name      string
	Responses [][]*big.Rat
}

// Problem is a fully specified adaptive probing instance. Priors[s] is the
// exact rational prior probability of secret s; the slice must sum to 1.
// Rounds is the maximum number of interactions (0 means guess immediately).
type Problem struct {
	Priors []*big.Rat
	Probes []Probe
	Rounds int
}

// Node is one node of the optimal adaptive strategy tree.
//
// A leaf has Probe == -1 and Guess set to the secret index the host should
// commit to. An internal node names the probe to issue; its Children cover
// every response that has non-zero probability under the current belief.
// ChildProbability is the exact probability of taking that branch.
type Node struct {
	Probe            int
	Guess            int
	Children         []*Node
	ChildProbability []*big.Rat
	Belief           []*big.Rat
	Depth            int
}

// Result is the output of Solve.
type Result struct {
	// MaxSuccessProbability is the exact probability that the returned
	// strategy guesses the secret correctly, optimized over all adaptive
	// policies within the round budget (stopping early is allowed).
	MaxSuccessProbability *big.Rat
	// Root is the head of the optimal strategy tree.
	Root *Node
	// BeliefStatesVisited counts distinct memoized (belief, rounds-left)
	// states evaluated by the exact dynamic program.
	BeliefStatesVisited int
}

type memoKey struct {
	depth  int
	belief string
}

type memoEntry struct {
	value *big.Rat
	probe int // -1 means stop and guess argmaxSecret(belief)
}

type solver struct {
	priors []*big.Rat
	probes []Probe
	memo   map[memoKey]*memoEntry
	nodes  map[memoKey]*Node
}

// Solve computes the maximum achievable probability of correctly identifying
// the secret together with an optimal adaptive strategy tree. All arithmetic
// uses exact rational numbers; no floating point is involved.
//
// Responses are conditionally independent across rounds given the secret, so
// each round is a Bayesian belief update followed by an optimal probe choice.
// Repeating a probe is allowed and handled naturally.
func Solve(p Problem) (*Result, error) {
	if err := validate(p); err != nil {
		return nil, err
	}

	s := &solver{
		priors: cloneRats(p.Priors),
		probes: p.Probes,
		memo:   make(map[memoKey]*memoEntry),
		nodes:  make(map[memoKey]*Node),
	}

	root := s.build(p.Rounds, s.priors, 0)
	return &Result{
		MaxSuccessProbability: new(big.Rat).Set(s.ensure(p.Rounds, s.priors).value),
		Root:                  root,
		BeliefStatesVisited:   len(s.memo),
	}, nil
}

// ensure returns the memoized optimum (value and best action) for
// (roundsLeft, b), computing it once on first request.
func (s *solver) ensure(roundsLeft int, b []*big.Rat) *memoEntry {
	if roundsLeft <= 0 {
		return &memoEntry{value: stopValue(b), probe: -1}
	}

	key := memoKey{depth: roundsLeft, belief: beliefKey(b)}
	if entry, ok := s.memo[key]; ok {
		return entry
	}

	// Option 1: stop and commit to the most probable secret.
	best := new(big.Rat).Set(stopValue(b))
	bestProbe := -1

	// Option 2: issue a probe, then continue optimally on each branch.
	for p := range s.probes {
		candidate := new(big.Rat)
		for r := range s.probes[p].Responses {
			total, next := s.update(b, p, r)
			if total.Sign() == 0 {
				// Impossible response under the current belief.
				continue
			}
			candidate.Add(candidate, new(big.Rat).Mul(total, s.ensure(roundsLeft-1, next).value))
		}
		if candidate.Cmp(best) > 0 {
			best.Set(candidate)
			bestProbe = p
		}
	}

	entry := &memoEntry{value: best, probe: bestProbe}
	s.memo[key] = entry
	return entry
}

// build reconstructs the strategy tree attaining the memoized optimum.
func (s *solver) build(roundsLeft int, b []*big.Rat, depth int) *Node {
	key := memoKey{depth: roundsLeft, belief: beliefKey(b)}
	if roundsLeft > 0 {
		if cached, ok := s.nodes[key]; ok {
			return cached
		}
	}

	node := &Node{
		Probe:  -1,
		Guess:  argmaxSecret(b),
		Belief: cloneRats(b),
		Depth:  depth,
	}
	if roundsLeft <= 0 {
		return node
	}

	entry := s.ensure(roundsLeft, b)
	if entry.probe == -1 {
		// No probe strictly improves on guessing now; stop early.
		s.nodes[key] = node
		return node
	}

	p := entry.probe
	node.Probe = p
	for r := range s.probes[p].Responses {
		total, next := s.update(b, p, r)
		if total.Sign() == 0 {
			continue
		}
		node.ChildProbability = append(node.ChildProbability, new(big.Rat).Set(total))
		node.Children = append(node.Children, s.build(roundsLeft-1, next, depth+1))
	}

	s.nodes[key] = node
	return node
}

// update applies Bayes' rule for response r of probe p. It returns the exact
// total response probability under b and the normalized posterior belief.
// The input belief is not modified.
func (s *solver) update(b []*big.Rat, p, r int) (*big.Rat, []*big.Rat) {
	numSecrets := len(b)
	weighted := make([]*big.Rat, numSecrets)
	total := new(big.Rat)
	for secret := 0; secret < numSecrets; secret++ {
		like := s.probes[p].Responses[r][secret]
		w := new(big.Rat).Mul(b[secret], like)
		weighted[secret] = w
		total.Add(total, w)
	}

	posterior := make([]*big.Rat, numSecrets)
	if total.Sign() == 0 {
		// Zero-probability branch: no valid posterior exists. Return an
		// all-zero slice; callers skip the branch because total == 0.
		for secret := range posterior {
			posterior[secret] = new(big.Rat)
		}
		return total, posterior
	}

	for secret := 0; secret < numSecrets; secret++ {
		posterior[secret] = new(big.Rat).Quo(weighted[secret], total)
	}
	return total, posterior
}

// stopValue is the success probability of guessing immediately: the largest
// entry of the belief.
func stopValue(b []*big.Rat) *big.Rat {
	return new(big.Rat).Set(b[argmaxSecret(b)])
}

// argmaxSecret returns the lowest index attaining max(b), giving deterministic
// tie-breaking for guesses.
func argmaxSecret(b []*big.Rat) int {
	best := 0
	for i := 1; i < len(b); i++ {
		if b[i].Cmp(b[best]) > 0 {
			best = i
		}
	}
	return best
}

func beliefKey(b []*big.Rat) string {
	parts := make([]string, len(b))
	for i, r := range b {
		// RatString canonicalizes via the reduced fraction a/b.
		parts[i] = r.RatString()
	}
	return strings.Join(parts, "|")
}

func cloneRats(in []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(in))
	for i, r := range in {
		out[i] = new(big.Rat).Set(r)
	}
	return out
}

func validate(p Problem) error {
	n := len(p.Priors)
	if n == 0 {
		return errors.New("netsec: at least one secret is required")
	}
	if n > MaxSecrets {
		return fmt.Errorf("netsec: %d secrets exceed MaxSecrets (%d)", n, MaxSecrets)
	}
	if p.Rounds < 0 {
		return errors.New("netsec: Rounds must be non-negative")
	}
	if p.Rounds > MaxRounds {
		return fmt.Errorf("netsec: %d rounds exceed MaxRounds (%d)", p.Rounds, MaxRounds)
	}

	priorSum := new(big.Rat)
	for i, pr := range p.Priors {
		if pr == nil {
			return fmt.Errorf("netsec: prior for secret %d is nil", i)
		}
		if pr.Sign() < 0 {
			return fmt.Errorf("netsec: prior for secret %d is negative", i)
		}
		priorSum.Add(priorSum, pr)
	}
	if priorSum.Cmp(oneRat) != 0 {
		return fmt.Errorf("netsec: priors must sum to 1, got %s", priorSum.RatString())
	}

	if len(p.Probes) > MaxProbes {
		return fmt.Errorf("netsec: %d probes exceed MaxProbes (%d)", len(p.Probes), MaxProbes)
	}

	for pi, probe := range p.Probes {
		nr := len(probe.Responses)
		if nr == 0 {
			return fmt.Errorf("netsec: probe %d has no responses", pi)
		}
		if nr > MaxResponses {
			return fmt.Errorf("netsec: probe %d has %d responses, exceeding MaxResponses (%d)", pi, nr, MaxResponses)
		}
		columnSum := make([]*big.Rat, n)
		for s := range columnSum {
			columnSum[s] = new(big.Rat)
		}
		for r, row := range probe.Responses {
			if len(row) != n {
				return fmt.Errorf("netsec: probe %d response %d has %d entries, want %d", pi, r, len(row), n)
			}
			for s, prob := range row {
				if prob == nil {
					return fmt.Errorf("netsec: probe %d response %d secret %d probability is nil", pi, r, s)
				}
				if prob.Sign() < 0 {
					return fmt.Errorf("netsec: negative probability in probe %d response %d secret %d", pi, r, s)
				}
				if prob.Cmp(oneRat) > 0 {
					return fmt.Errorf("netsec: probability above 1 in probe %d response %d secret %d", pi, r, s)
				}
				columnSum[s].Add(columnSum[s], prob)
			}
		}
		for s, sum := range columnSum {
			if sum.Cmp(oneRat) != 0 {
				return fmt.Errorf("netsec: probe %d probabilities for secret %d sum to %s, want 1", pi, s, sum.RatString())
			}
		}
	}
	return nil
}
