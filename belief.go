package plango

import (
	"fmt"
	"sort"
	"strings"
)

// MaxWorlds bounds the finite world set: a Belief is a bitmask over worlds.
const MaxWorlds = 64

// World is one possible world: a full capability state plus the observation
// token the agent receives upon entering it. Worlds whose states differ may
// share a token, and are then indistinguishable after the same history.
type World struct {
	State State
	Obs   string
}

// NDAction is a nondeterministic action: its outcome is revealed only after
// execution, via the observation token of the successor world.
// Trans[w] lists the possible successor world indices when the action is
// executed in world w; an empty slice means the action is not applicable in w.
type NDAction struct {
	Name  string
	Trans [][]int
}

// Belief is a set of world indices the agent considers possible.
type Belief uint64

// BWorlds packs world indices into a Belief.
func BWorlds(idxs ...int) Belief {
	var b Belief
	for _, i := range idxs {
		b |= 1 << uint(i)
	}
	return b
}

// Contains reports whether world i is in the belief.
func (b Belief) Contains(i int) bool { return b&(1<<uint(i)) != 0 }

// Worlds returns the sorted world indices in the belief.
func (b Belief) Worlds() []int {
	var out []int
	for i := 0; i < MaxWorlds; i++ {
		if b.Contains(i) {
			out = append(out, i)
		}
	}
	return out
}

func (b Belief) String() string {
	parts := make([]string, 0)
	for _, w := range b.Worlds() {
		parts = append(parts, fmt.Sprintf("w%d", w))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// NDProblem is a partially observable planning instance over finite worlds.
type NDProblem struct {
	Worlds  []World
	Actions []NDAction
	Goal    State   // capability mask every world must satisfy at the end
	Forbidden []State // masks no world state may ever contain
	Initial Belief
}

func (p NDProblem) forbiddenState(s State) bool {
	for _, m := range p.Forbidden {
		if s.Has(m) {
			return true
		}
	}
	return false
}

// validBelief reports whether no world in b is in a forbidden state.
func (p NDProblem) validBelief(b Belief) bool {
	for _, w := range b.Worlds() {
		if p.forbiddenState(p.Worlds[w].State) {
			return false
		}
	}
	return true
}

// goalBelief reports whether every world in b satisfies the goal.
func (p NDProblem) goalBelief(b Belief) bool {
	for _, w := range b.Worlds() {
		if !p.Worlds[w].State.Has(p.Goal) {
			return false
		}
	}
	return true
}

// children applies a to belief b. It returns nil if the action is not
// applicable in every possible world or any outcome is unsafe; otherwise it
// groups the possible successors by observation token. The agent cannot
// distinguish worlds inside one group, so each group is one successor belief.
func (p NDProblem) children(b Belief, a NDAction) map[string]Belief {
	out := make(map[string]Belief)
	for _, w := range b.Worlds() {
		succs := a.Trans[w]
		if len(succs) == 0 {
			return nil
		}
		for _, s := range succs {
			if s < 0 || s >= len(p.Worlds) || p.forbiddenState(p.Worlds[s].State) {
				return nil
			}
			out[p.Worlds[s].Obs] |= 1 << uint(s)
		}
	}
	return out
}

// Policy is a contingent plan tree. Because it is keyed by observation
// history, identical histories necessarily select the same action.
type Policy struct {
	Belief   Belief             // worlds consistent with the observation history
	Action   string             // action to execute; "" at a goal leaf
	Worst    int                // worst-case remaining steps under this subtree
	Branches map[string]*Policy // one child per possible observation
}

// String renders the policy tree with indentation.
func (pol *Policy) String() string {
	var sb strings.Builder
	pol.format(&sb, 0)
	return sb.String()
}

func (pol *Policy) format(sb *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	if pol.Action == "" {
		fmt.Fprintf(sb, "%s%s goal reached (worst %d)\n", indent, pol.Belief, pol.Worst)
		return
	}
	fmt.Fprintf(sb, "%s%s do %q (worst %d)\n", indent, pol.Belief, pol.Action, pol.Worst)
	obs := make([]string, 0, len(pol.Branches))
	for o := range pol.Branches {
		obs = append(obs, o)
	}
	sort.Strings(obs)
	for _, o := range obs {
		fmt.Fprintf(sb, "%s  observe %q:\n", indent, o)
		pol.Branches[o].format(sb, depth+2)
	}
}

// Solution is the result of SolveND.
type Solution struct {
	Solvable   bool
	Policy     *Policy // nil when unsolvable
	DeadWorlds []int   // worlds that cannot be brought to the goal safely
	// Cost maps every reachable belief to its minimal worst-case steps;
	// beliefs absent from Cost are unreachable from the initial belief or
	// any singleton.
	Cost map[Belief]int
}

const infinity = int(^uint(0) >> 1)

// SolveND computes a policy that brings every world of the initial belief to
// the goal safely, minimizing the worst-case number of steps. It runs a
// fixpoint over the finite belief space: cost 0 for all-goal beliefs, and
// cost 1 + max over observation branches otherwise. Unsolvable beliefs keep
// infinite cost, which both proves unsolvability and yields the dead worlds.
func SolveND(p NDProblem) Solution {
	if len(p.Worlds) > MaxWorlds {
		panic(fmt.Sprintf("plango: %d worlds exceed MaxWorlds=%d", len(p.Worlds), MaxWorlds))
	}
	sol := Solution{Cost: make(map[Belief]int)}
	if p.Initial == 0 || !p.validBelief(p.Initial) {
		sol.DeadWorlds = deadWorlds(p, nil)
		return sol
	}

	// Enumerate every belief reachable from the initial belief or from any
	// singleton (singletons identify per-world dead ends).
	reachable := make(map[Belief]bool)
	queue := []Belief{p.Initial}
	for w := range p.Worlds {
		b := Belief(1) << uint(w)
		if p.validBelief(b) {
			queue = append(queue, b)
		}
	}
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		if reachable[b] {
			continue
		}
		reachable[b] = true
		for _, a := range p.Actions {
			for _, child := range p.children(b, a) {
				if !reachable[child] {
					queue = append(queue, child)
				}
			}
		}
	}

	// Value iteration to a fixpoint: costs only ever decrease from infinity.
	cost := make(map[Belief]int, len(reachable))
	for b := range reachable {
		if p.goalBelief(b) {
			cost[b] = 0
		} else {
			cost[b] = infinity
		}
	}
	for changed := true; changed; {
		changed = false
		for b := range reachable {
			if cost[b] == 0 {
				continue
			}
			best := infinity
			for _, a := range p.Actions {
				kids := p.children(b, a)
				if kids == nil {
					continue
				}
				worst := 0
				ok := true
				for _, child := range kids {
					c, known := cost[child]
					if !known || c == infinity {
						ok = false
						break
					}
					if c > worst {
						worst = c
					}
				}
				if ok && worst+1 < best {
					best = worst + 1
				}
			}
			if best < cost[b] {
				cost[b] = best
				changed = true
			}
		}
	}
	sol.Cost = cost
	sol.DeadWorlds = deadWorlds(p, cost)

	c, known := cost[p.Initial]
	if !known || c == infinity {
		return sol
	}
	sol.Solvable = true
	sol.Policy = buildPolicy(p, cost, p.Initial)
	return sol
}

// deadWorlds lists worlds whose singleton belief is unsolvable. When cost is
// nil (invalid initial belief) every world is checked structurally instead.
func deadWorlds(p NDProblem, cost map[Belief]int) []int {
	var dead []int
	for w := range p.Worlds {
		b := Belief(1) << uint(w)
		if p.forbiddenState(p.Worlds[w].State) {
			dead = append(dead, w)
			continue
		}
		if cost != nil {
			if c, ok := cost[b]; !ok || c == infinity {
				dead = append(dead, w)
			}
		}
	}
	return dead
}

// buildPolicy extracts the optimal policy tree from the cost fixpoint.
// Costs strictly decrease along chosen branches, so recursion terminates.
func buildPolicy(p NDProblem, cost map[Belief]int, b Belief) *Policy {
	pol := &Policy{Belief: b, Worst: cost[b]}
	if cost[b] == 0 {
		return pol
	}
	for _, a := range p.Actions {
		kids := p.children(b, a)
		if kids == nil {
			continue
		}
		worst := 0
		ok := true
		for _, child := range kids {
			c, known := cost[child]
			if !known || c == infinity {
				ok = false
				break
			}
			if c > worst {
				worst = c
			}
		}
		if ok && worst+1 == cost[b] {
			pol.Action = a.Name
			pol.Branches = make(map[string]*Policy, len(kids))
			for obs, child := range kids {
				pol.Branches[obs] = buildPolicy(p, cost, child)
			}
			return pol
		}
	}
	return pol
}

// VerifyPolicy cross-checks a policy with a full belief-state search: it
// replays every world of the initial belief through the tree, confirming
// that (1) each world arrives exactly at the node whose belief contains it
// (so identical observation histories pick identical actions), (2) every
// branch and terminal state is safe and satisfies the goal, and (3) the
// announced worst-case step counts match the real ones.
func VerifyPolicy(p NDProblem, pol *Policy) error {
	if pol == nil {
		return fmt.Errorf("nil policy")
	}
	if pol.Belief != p.Initial {
		return fmt.Errorf("root belief %s does not match initial belief %s", pol.Belief, p.Initial)
	}
	arrivals := make(map[*Policy]Belief)
	worst := 0
	for _, w := range p.Initial.Worlds() {
		depth, err := walkPolicy(p, pol, w, arrivals)
		if err != nil {
			return err
		}
		if depth > worst {
			worst = depth
		}
	}
	if worst != pol.Worst {
		return fmt.Errorf("root worst-case mismatch: policy claims %d, replay took %d", pol.Worst, worst)
	}
	for node, got := range arrivals {
		if got != node.Belief {
			return fmt.Errorf("belief drift at node %s: replay arrived with %s", node.Belief, got)
		}
	}
	return nil
}

// walkPolicy replays a single world through the policy tree, following the
// branch selected by each successor's observation, and returns the number of
// steps the world needed to reach a goal leaf.
func walkPolicy(p NDProblem, node *Policy, w int, arrivals map[*Policy]Belief) (int, error) {
	if !node.Belief.Contains(w) {
		return 0, fmt.Errorf("world w%d escaped its belief node %s", w, node.Belief)
	}
	arrivals[node] |= 1 << uint(w)
	if !p.validBelief(node.Belief) {
		return 0, fmt.Errorf("belief %s contains a forbidden world state", node.Belief)
	}
	if node.Action == "" {
		if !p.Worlds[w].State.Has(p.Goal) {
			return 0, fmt.Errorf("world w%d stopped at %s without satisfying goal %s", w, p.Worlds[w].State, p.Goal)
		}
		return 0, nil
	}
	var action *NDAction
	for i := range p.Actions {
		if p.Actions[i].Name == node.Action {
			action = &p.Actions[i]
			break
		}
	}
	if action == nil {
		return 0, fmt.Errorf("policy names unknown action %q", node.Action)
	}
	succs := action.Trans[w]
	if len(succs) == 0 {
		return 0, fmt.Errorf("action %q not applicable in world w%d", node.Action, w)
	}
	worst := 0
	for _, s := range succs {
		if s < 0 || s >= len(p.Worlds) {
			return 0, fmt.Errorf("action %q: bad successor %d", node.Action, s)
		}
		if p.forbiddenState(p.Worlds[s].State) {
			return 0, fmt.Errorf("action %q leads world w%d into forbidden state %s", node.Action, w, p.Worlds[s].State)
		}
		child, ok := node.Branches[p.Worlds[s].Obs]
		if !ok {
			return 0, fmt.Errorf("no branch for observation %q after %q", p.Worlds[s].Obs, node.Action)
		}
		d, err := walkPolicy(p, child, s, arrivals)
		if err != nil {
			return 0, err
		}
		if d+1 > worst {
			worst = d + 1
		}
	}
	if worst != node.Worst {
		return 0, fmt.Errorf("node %s worst-case mismatch: claims %d, replay took %d", node.Belief, node.Worst, worst)
	}
	return worst, nil
}
