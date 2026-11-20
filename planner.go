package updatestrategy

import (
	"fmt"
	"sort"
	"strings"
)

// Target names the committed update goal: every reachable world must run
// TargetImage on an authenticated slot with at least MinCounter rollback
// counter, and the slot must be confirmed so that a later reboot cannot
// silently revert it.
type Target struct {
	TargetImage Digest
	MinCounter  int
}

// Reached reports whether w satisfies the full update goal.
func (t Target) Reached(w World) bool {
	return w.Slots[w.Active] == t.TargetImage &&
		w.Counter >= t.MinCounter &&
		w.Pending == w.Active
}

// Problem is the complete planning input. Components, image digests, the
// signing root (represented here by caller-chosen digest identity) and the
// counter floor are all supplied by the host; the planner invents none of them.
type Problem struct {
	// InitialBelief is every persistent world compatible with the initial
	// public readback. It must contain at least one world, and every world in
	// it must currently be offline bootable.
	InitialBelief []World
	Actions       []Action
	Target        Target
	// MaxDepth bounds the search; 0 selects a generous default. It does not
	// relax any guarantee, only rejects pathological inputs.
	MaxDepth int
}

// Belief is a set of concrete worlds jointly compatible with the observation
// history. Decisions are keyed on Belief, never on a single World.
type Belief struct {
	worlds []World
	key    string
}

// MakeBelief dedupes and normalises a world set.
func MakeBelief(worlds []World) Belief {
	seen := make(map[string]World, len(worlds))
	for _, w := range worlds {
		seen[w.ID()] = w
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]World, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, seen[k])
	}
	return Belief{worlds: ordered, key: strings.Join(keys, " || ")}
}

func (b Belief) Key() string     { return b.key }
func (b Belief) Worlds() []World { return append([]World(nil), b.worlds...) }
func (b Belief) Size() int       { return len(b.worlds) }

// ObsGroup is one observation branch after choosing an action: all worlds that
// emit this observation, merged into a single successor belief.
type ObsGroup struct {
	Obs      Observation
	Belief   Belief
	Outcomes []Outcome
}

// Branch returns the partition of an action's outcomes by public observation.
// Worlds sharing one observation are deliberately merged, which is what forces
// the policy to work for every hidden persistent result at once.
func Branch(w Belief, act Action) []ObsGroup {
	groups := map[string]*ObsGroup{}
	var order []Observation
	obsKey := func(o Observation) string {
		return fmt.Sprintf("%d|%s|%d|%t|%d", o.Kind, o.ActiveDigest, o.ActiveSlot, o.Ack, o.Counter)
	}
	for _, w0 := range w.worlds {
		for _, oc := range act.Apply(w0) {
			k := obsKey(oc.Obs)
			g := groups[k]
			if g == nil {
				g = &ObsGroup{Obs: oc.Obs}
				groups[k] = g
				order = append(order, oc.Obs)
			}
			g.Outcomes = append(g.Outcomes, oc)
		}
	}

	// Deterministic branch order: observations in the order first encountered,
	// which itself follows the deterministic world ordering inside the belief.
	out := make([]ObsGroup, 0, len(order))
	seen := map[string]bool{}
	for _, ob := range order {
		k := obsKey(ob)
		if seen[k] {
			continue
		}
		seen[k] = true
		g := groups[k]
		worlds := make([]World, 0, len(g.Outcomes))
		for _, oc := range g.Outcomes {
			worlds = append(worlds, oc.World)
		}
		g.Belief = MakeBelief(worlds)
		out = append(out, *g)
	}
	return out
}

// Refusal explains why the host cannot be given a guaranteed policy.
type Refusal struct {
	// Reason is the machine-readable category.
	Reason RefusalReason
	// AtBelief is the belief at which every remaining action fails.
	AtBelief Belief
	// FailedAction names the last action attempted per world, with the concrete
	// reason each hidden world could not be served. A non-empty divergent entry
	// is the canonical "indistinguishable but require different actions" case.
	FailedActions []ActionFailure
	Message       string
}

// RefusalReason enumerates structural planning failures.
type RefusalReason int

const (
	RefusalUnsafeAction RefusalReason = iota
	RefusalGoalUnreachable
	RefusalDivergentBelief
)

func (r RefusalReason) String() string {
	switch r {
	case RefusalUnsafeAction:
		return "unsafe-action"
	case RefusalGoalUnreachable:
		return "goal-unreachable"
	case RefusalDivergentBelief:
		return "indistinguishable-divergent-action"
	default:
		return "unknown"
	}
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("%s: %s: %s (belief=%s)", ErrPlanningRefused, r.Reason, r.Message, r.AtBelief.key)
}

func (r *Refusal) Unwrap() error { return ErrPlanningRefused }

// ActionFailure pairs an action with the safety/goal failure it causes for at
// least one world inside an indistinguishable belief.
type ActionFailure struct {
	Action  Action
	WorldID string
	Detail  string
}

// PolicyNode binds one belief to the single action the host may take from it
// and to the successor policy for every public observation that can follow.
type PolicyNode struct {
	Belief    Belief
	Action    Action
	Branches  map[Observation]*PolicyNode
	WorstCost int
	IsGoal    bool
}

// Policy is the complete observation-branching decision tree.
type Policy struct {
	Root      *PolicyNode
	Target    Target
	WorstOps  int
	nodeCount int
}

// NodeCount reports how many distinct beliefs the solved policy contains.
func (p *Policy) NodeCount() int { return p.nodeCount }

type solveState struct {
	problem  Problem
	memo     map[string]*solveResult
	active   map[string]bool
	depth    int
	maxDepth int
}

type solveResult struct {
	node *PolicyNode
	// refusals collect why an action failed, used only when every action fails.
	refusal *Refusal
}

// Plan computes the minimum worst-case-operation observation policy. It
// returns a Refusal wrapped around ErrPlanningRefused when no action can
// guarantee the target for every world of the current belief.
func Plan(p Problem) (*Policy, error) {
	if len(p.InitialBelief) == 0 {
		return nil, fmt.Errorf("updatestrategy: InitialBelief must contain at least one world")
	}
	for _, w := range p.InitialBelief {
		if !w.Bootable(w.Active) {
			return nil, fmt.Errorf("updatestrategy: initial world %s is not offline bootable", w.ID())
		}
	}
	maxDepth := p.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 24
	}
	st := &solveState{
		problem:  p,
		memo:     map[string]*solveResult{},
		active:   map[string]bool{},
		maxDepth: maxDepth,
	}
	root := MakeBelief(p.InitialBelief)
	res := st.solve(root)
	if res.refusal != nil {
		return nil, res.refusal
	}
	return &Policy{Root: res.node, Target: p.Target, WorstOps: res.node.WorstCost, nodeCount: len(st.memo)}, nil
}

func (s *solveState) solve(b Belief) *solveResult {
	if r, ok := s.memo[b.key]; ok {
		return r
	}
	goalNode := s.goalNode(b)
	if goalNode != nil {
		r := &solveResult{node: goalNode}
		s.memo[b.key] = r
		return r
	}
	if s.depth >= s.maxDepth {
		return s.fail(b, nil, "search depth exhausted")
	}
	s.active[b.key] = true
	s.depth++
	defer func() {
		s.depth--
		delete(s.active, b.key)
	}()

	var best *solveResult
	var failures []ActionFailure
	for _, act := range s.problem.Actions {
		safeAct, ok := act.(safeAction)
		if !ok {
			failures = append(failures, ActionFailure{Action: act, Detail: "action does not declare safety"})
			continue
		}
		unsafe := false
		for _, w := range b.worlds {
			if err := safeAct.SafeIn(w); err != nil {
				failures = append(failures, ActionFailure{Action: act, WorldID: w.ID(), Detail: err.Error()})
				unsafe = true
			}
		}
		if unsafe {
			continue
		}
		groups := Branch(b, act)
		node := &PolicyNode{Belief: b, Action: act, Branches: map[Observation]*PolicyNode{}}
		worst := 1
		feasible := true
		var branchFailures []ActionFailure
		for _, g := range groups {
			if g.Belief.key == b.key {
				// A self-loop cannot make progress toward the target and would
				// only inflate the worst-case cost; an optimal policy never
				// takes it, so treat it as an infeasible branch.
				branchFailures = append(branchFailures, ActionFailure{Action: act, Detail: "action produces no distinguishing observation and no state progress"})
				feasible = false
				break
			}
			if hasDeadBurnWorld(g.Belief, s.problem.Target) {
				branchFailures = append(branchFailures, ActionFailure{Action: act, Detail: "branch leaves a world whose counter already exceeds the target floor; further burns are impossible"})
				feasible = false
				break
			}
			if s.active[g.Belief.key] {
				// A non-goal loop cannot reduce worst-case cost; reject it so
				// policies always terminate.
				branchFailures = append(branchFailures, ActionFailure{Action: act, Detail: "non-terminating observation loop"})
				feasible = false
				break
			}
			sub := s.solve(g.Belief)
			if sub.refusal != nil {
				feasible = false
				branchFailures = append(branchFailures, sub.refusal.FailedActions...)
				break
			}
			node.Branches[g.Obs] = sub.node
			if c := 1 + sub.node.WorstCost; c > worst {
				worst = c
			}
		}
		if !feasible {
			failures = append(failures, branchFailures...)
			continue
		}
		node.WorstCost = worst
		candidate := &solveResult{node: node}
		if best == nil || node.WorstCost < best.node.WorstCost {
			best = candidate
		}
	}

	if best == nil {
		return s.fail(b, failures, "no action guarantees the target for every world in the belief")
	}
	s.memo[b.key] = best
	return best
}

func (s *solveState) goalNode(b Belief) *PolicyNode {
	allGoal := true
	for _, w := range b.worlds {
		if !s.problem.Target.Reached(w) {
			allGoal = false
			break
		}
	}
	if allGoal {
		return &PolicyNode{Belief: b, IsGoal: true, WorstCost: 0}
	}
	return nil
}

func (s *solveState) fail(b Belief, failures []ActionFailure, msg string) *solveResult {
	reason := RefusalGoalUnreachable
	if hasDivergent(failures) {
		reason = RefusalDivergentBelief
	}
	r := &solveResult{refusal: &Refusal{
		Reason:        reason,
		AtBelief:      b,
		FailedActions: failures,
		Message:       msg,
	}}
	s.memo[b.key] = r
	return r
}

func hasDivergent(failures []ActionFailure) bool {
	seen := map[string]bool{}
	for _, f := range failures {
		if f.WorldID != "" {
			seen[f.Action.Name()] = true
		}
	}
	return len(seen) > 0
}

// hasDeadBurnWorld reports whether the belief contains a world whose counter
// already exceeds the target floor while another world still needs a burn.
// Such a belief cannot be served by any uniform counter action and is dead:
// burning again is illegal in the overshoot world, while not burning strands
// the low world. This is the structural "early counter burn removed the last
// recovery path" condition, detected without peeking at hidden contents.
func hasDeadBurnWorld(b Belief, target Target) bool {
	over, under := false, false
	for _, w := range b.worlds {
		switch {
		case w.Counter > target.MinCounter:
			over = true
		case w.Counter < target.MinCounter:
			under = true
		}
	}
	return over && under
}
