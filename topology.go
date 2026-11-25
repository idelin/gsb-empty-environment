package handshake

import (
	"fmt"
	"sort"

	"prescreen.local/ps-802b9bdc43/bdd"
)

// Strength ranks an outcome. Reject is weaker than every authentication level.
// A custom rank may be supplied to NewTopology when authentication levels do
// not follow their natural numeric ordering.
type Strength func(outcome Outcome) int

// DefaultStrength treats Reject as 0 and authentication level N as N.
func DefaultStrength(outcome Outcome) int {
	if outcome.IsReject() {
		return 0
	}
	return outcome.Level
}

// NodeConfig holds the old and new ordered rule sets for one frontend node.
type NodeConfig struct {
	NodeID int
	Old    RuleSet
	New    RuleSet
}

// Topology describes the nodes and the client fallback order.
type Topology struct {
	manager  *bdd.Manager
	nodes    map[int]NodeConfig
	fallback []int
	legal    bdd.BDD
	strength Strength

	cache map[topologyCacheKey]map[Outcome]bdd.BDD
}

type topologyCacheKey struct {
	state uint16
	start int
}

// MaxNodes is the release-size limit specified for the scheduler.
const MaxNodes = 14

// NewTopology validates the node and fallback configuration and builds a
// reusable symbolic topology. legal is the ordered BDD of legal capability
// vectors; pass bdd.One when every vector is legal.
func NewTopology(manager *bdd.Manager, nodes []NodeConfig, fallbackSequence []int, legal bdd.BDD, strength Strength) (*Topology, error) {
	if manager == nil {
		return nil, fmt.Errorf("handshake: nil BDD manager")
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("handshake: at least one node is required")
	}
	if len(nodes) > MaxNodes {
		return nil, fmt.Errorf("handshake: at most %d nodes are supported, got %d", MaxNodes, len(nodes))
	}
	if len(fallbackSequence) == 0 {
		return nil, fmt.Errorf("handshake: fallback sequence must not be empty")
	}
	byID := map[int]NodeConfig{}
	for _, node := range nodes {
		if _, exists := byID[node.NodeID]; exists {
			return nil, fmt.Errorf("handshake: duplicate node id %d", node.NodeID)
		}
		byID[node.NodeID] = node
	}
	seen := map[int]bool{}
	for _, nodeID := range fallbackSequence {
		if _, ok := byID[nodeID]; !ok {
			return nil, fmt.Errorf("handshake: fallback references unknown node %d", nodeID)
		}
		if seen[nodeID] {
			return nil, fmt.Errorf("handshake: fallback contains duplicate node %d", nodeID)
		}
		seen[nodeID] = true
	}
	if strength == nil {
		strength = DefaultStrength
	}
	return &Topology{
		manager:  manager,
		nodes:    byID,
		fallback: append([]int(nil), fallbackSequence...),
		legal:    legal,
		strength: strength,
		cache:    map[topologyCacheKey]map[Outcome]bdd.BDD{},
	}, nil
}

// Manager returns the underlying ordered BDD manager.
func (t *Topology) Manager() *bdd.Manager { return t.manager }

// Legal returns the legal-capability BDD.
func (t *Topology) Legal() bdd.BDD { return t.legal }

// FallbackSequence returns a copy of the fallback node order.
func (t *Topology) FallbackSequence() []int { return append([]int(nil), t.fallback...) }

// Node returns the configuration of one node.
func (t *Topology) Node(id int) (NodeConfig, bool) {
	node, ok := t.nodes[id]
	return node, ok
}

// NodeIDs returns node ids in ascending order.
func (t *Topology) NodeIDs() []int {
	ids := make([]int, 0, len(t.nodes))
	for id := range t.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (t *Topology) stateMask(state SwitchSet) uint16 {
	var mask uint16
	for index, id := range t.NodeIDs() {
		if state[id] {
			mask |= 1 << index
		}
	}
	return mask
}

// SwitchSet records which nodes currently use their new rule set.
type SwitchSet map[int]bool

// NewSwitchSet creates an empty switch set.
func NewSwitchSet() SwitchSet { return SwitchSet{} }

// SwitchSetAll returns the fully switched state, which is the all-new endpoint.
func SwitchSetAll(t *Topology) SwitchSet {
	state := NewSwitchSet()
	for id := range t.nodes {
		state[id] = true
	}
	return state
}

// With returns a copy containing the given additional switched nodes.
func (s SwitchSet) With(ids ...int) SwitchSet {
	next := SwitchSet{}
	for id, switched := range s {
		next[id] = switched
	}
	for _, id := range ids {
		next[id] = true
	}
	return next
}

// Equal reports whether two switch sets contain the same nodes.
func (s SwitchSet) Equal(other SwitchSet) bool {
	if len(s) != len(other) {
		return false
	}
	for id := range s {
		if !other[id] {
			return false
		}
	}
	return true
}

// IDs returns the switched node ids in ascending order.
func (s SwitchSet) IDs() []int {
	ids := make([]int, 0, len(s))
	for id, switched := range s {
		if switched {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func (t *Topology) effective(nodeID int, state SwitchSet) RuleSet {
	node := t.nodes[nodeID]
	if state[nodeID] {
		return node.New
	}
	return node.Old
}

// EffectiveRules returns the rule set currently in force at a node.
func (t *Topology) EffectiveRules(nodeID int, state SwitchSet) (RuleSet, error) {
	if _, ok := t.nodes[nodeID]; !ok {
		return RuleSet{}, fmt.Errorf("handshake: unknown node %d", nodeID)
	}
	return t.effective(nodeID, state), nil
}

// Evaluate runs first-match evaluation and fallback from fallbackSequence[start].
// A fresh connection uses start=0. A reconnect with a session ticket starts at
// the ticket's target node index and is revalidated by that node's currently
// effective rules. Only a reject advances to the next node.
func (t *Topology) Evaluate(state SwitchSet, start int, capabilities []bool) (Outcome, error) {
	if start < 0 || start >= len(t.fallback) {
		return RejectOutcome(), fmt.Errorf("handshake: fallback start %d out of range [0,%d)", start, len(t.fallback))
	}
	legal, err := t.manager.Eval(t.legal, capabilities)
	if err != nil {
		return RejectOutcome(), err
	}
	if !legal {
		return RejectOutcome(), nil
	}
	for index := start; index < len(t.fallback); index++ {
		rules := t.effective(t.fallback[index], state)
		outcome, err := rules.Evaluate(t.manager, capabilities)
		if err != nil {
			return RejectOutcome(), err
		}
		if !outcome.IsReject() {
			return outcome, nil
		}
	}
	return RejectOutcome(), nil
}

// SymbolicResult returns, for every distinct outcome, the BDD of legal
// capability vectors producing that outcome from fallbackSequence[start] under
// the given switched state. The BDDs partition the legal capability space.
func (t *Topology) SymbolicResult(state SwitchSet, start int) (map[Outcome]bdd.BDD, error) {
	if start < 0 || start >= len(t.fallback) {
		return nil, fmt.Errorf("handshake: fallback start %d out of range [0,%d)", start, len(t.fallback))
	}
	key := topologyCacheKey{state: t.stateMask(state), start: start}
	if cached, ok := t.cache[key]; ok {
		return cached, nil
	}

	reachable := make([]bdd.BDD, len(t.fallback))
	reachable[start] = t.legal
	for index := start + 1; index < len(t.fallback); index++ {
		rules := t.effective(t.fallback[index-1], state)
		reject := t.rejectDomain(rules)
		reachable[index] = t.manager.And(reachable[index-1], reject)
	}

	result := map[Outcome]bdd.BDD{}
	for index := start; index < len(t.fallback); index++ {
		rules := t.effective(t.fallback[index], state)
		for _, region := range rules.Regions(t.manager) {
			hit := t.manager.And(reachable[index], region.Cover)
			if t.manager.IsZero(hit) {
				continue
			}
			if region.Outcome.IsReject() {
				continue
			}
			result[region.Outcome] = t.manager.Or(result[region.Outcome], hit)
		}
	}
	accepted := bdd.Zero
	for _, cover := range result {
		accepted = t.manager.Or(accepted, cover)
	}
	denied := t.manager.And(reachable[len(t.fallback)-1], t.manager.Not(accepted))
	if !t.manager.IsZero(denied) {
		result[RejectOutcome()] = denied
	}
	t.cache[key] = result
	return result, nil
}

func (t *Topology) rejectDomain(rules RuleSet) bdd.BDD {
	reject, ok := rules.OutcomeBDDs(t.manager)[RejectOutcome()]
	if !ok {
		return bdd.Zero
	}
	return reject
}

// Violation identifies one capability vector that breaks a transition safety
// requirement, together with its old/new/intermediate fallback outcomes.
type Violation struct {
	Capabilities []bool
	State        SwitchSet
	Start        int
	OldOutcome   Outcome
	NewOutcome   Outcome
	MidOutcome   Outcome
	Kind         string
}

type violationParts struct {
	weakerThanBoth bdd.BDD
	bothConnect    bdd.BDD
}

func (t *Topology) violationParts(oldResult, newResult, midResult map[Outcome]bdd.BDD) violationParts {
	parts := violationParts{weakerThanBoth: bdd.Zero, bothConnect: bdd.Zero}
	for oldOutcome, oldBDD := range oldResult {
		oldStrength := t.strength(oldOutcome)
		for newOutcome, newBDD := range newResult {
			newStrength := t.strength(newOutcome)
			pairDomain := t.manager.And(oldBDD, newBDD)
			if t.manager.IsZero(pairDomain) {
				continue
			}
			for midOutcome, midBDD := range midResult {
				midStrength := t.strength(midOutcome)
				bothConnect := !oldOutcome.IsReject() && !newOutcome.IsReject()
				midRejects := midOutcome.IsReject()
				hit := t.manager.And(pairDomain, midBDD)
				if midStrength < oldStrength && midStrength < newStrength {
					parts.weakerThanBoth = t.manager.Or(parts.weakerThanBoth, hit)
				}
				if bothConnect && midRejects {
					parts.bothConnect = t.manager.Or(parts.bothConnect, hit)
				}
			}
		}
	}
	return parts
}

func (t *Topology) resultTriple(state SwitchSet, start int) (oldResult, newResult, midResult map[Outcome]bdd.BDD, err error) {
	midResult, err = t.SymbolicResult(state, start)
	if err != nil {
		return nil, nil, nil, err
	}
	oldResult, err = t.SymbolicResult(NewSwitchSet(), start)
	if err != nil {
		return nil, nil, nil, err
	}
	newResult, err = t.SymbolicResult(SwitchSetAll(t), start)
	if err != nil {
		return nil, nil, nil, err
	}
	if state.Equal(NewSwitchSet()) {
		oldResult = midResult
	}
	if state.Equal(SwitchSetAll(t)) {
		newResult = midResult
	}
	return oldResult, newResult, midResult, nil
}

// SmallestViolation checks one intermediate state and one fallback start. It
// returns the bit-order-smallest violating legal capability vector, or nil.
func (t *Topology) SmallestViolation(state SwitchSet, start int) (*Violation, error) {
	oldResult, newResult, midResult, err := t.resultTriple(state, start)
	if err != nil {
		return nil, err
	}
	parts := t.violationParts(oldResult, newResult, midResult)
	assignment, kind := t.smallestViolatingAssignment(parts)
	if assignment == nil {
		return nil, nil
	}
	return t.describeViolation(assignment, kind, state, start, oldResult, newResult, midResult)
}

func (t *Topology) smallestViolatingAssignment(parts violationParts) ([]bool, string) {
	bothConnect := t.manager.MinSolution(parts.bothConnect)
	weaker := t.manager.MinSolution(parts.weakerThanBoth)
	if bothConnect != nil && (weaker == nil || !lessBitOrder(weaker, bothConnect)) {
		return bothConnect, "reject-while-both-endpoints-connect"
	}
	if weaker != nil {
		return weaker, "weaker-than-both-endpoints"
	}
	return nil, ""
}

func (t *Topology) describeViolation(capabilities []bool, kind string, state SwitchSet, start int, oldResult, newResult, midResult map[Outcome]bdd.BDD) (*Violation, error) {
	oldOutcome, err := outcomeFor(t.manager, oldResult, capabilities)
	if err != nil {
		return nil, err
	}
	newOutcome, err := outcomeFor(t.manager, newResult, capabilities)
	if err != nil {
		return nil, err
	}
	midOutcome, err := outcomeFor(t.manager, midResult, capabilities)
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind = "weaker-than-both-endpoints"
	}
	return &Violation{
		Capabilities: append([]bool(nil), capabilities...),
		State:        state,
		Start:        start,
		OldOutcome:   oldOutcome,
		NewOutcome:   newOutcome,
		MidOutcome:   midOutcome,
		Kind:         kind,
	}, nil
}

func outcomeFor(manager *bdd.Manager, result map[Outcome]bdd.BDD, capabilities []bool) (Outcome, error) {
	for outcome, root := range result {
		matched, err := manager.Eval(root, capabilities)
		if err != nil {
			return RejectOutcome(), err
		}
		if matched {
			return outcome, nil
		}
	}
	return RejectOutcome(), nil
}

// HasLegalClient reports whether at least one legal capability vector exists.
func (t *Topology) HasLegalClient() bool { return !t.manager.IsZero(t.legal) }

// CheckStates verifies every reachable intermediate state at every fallback
// start (including fresh connections and every possible ticket target). It
// returns the bit-order-smallest violation across all inputs, or nil when the
// release is symbolically safe. With no legal client every safety property is
// vacuously true.
func (t *Topology) CheckStates(states []SwitchSet) (*Violation, error) {
	if !t.HasLegalClient() {
		return nil, nil
	}
	var smallest []bool
	var best *Violation
	for _, state := range states {
		for start := 0; start < len(t.fallback); start++ {
			oldResult, newResult, midResult, err := t.resultTriple(state, start)
			if err != nil {
				return nil, err
			}
			parts := t.violationParts(oldResult, newResult, midResult)
			candidate, kind := t.smallestViolatingAssignment(parts)
			if candidate == nil {
				continue
			}
			if smallest != nil && !lessBitOrder(candidate, smallest) {
				continue
			}
			smallest = append([]bool(nil), candidate...)
			best, err = t.describeViolation(candidate, kind, state, start, oldResult, newResult, midResult)
			if err != nil {
				return nil, err
			}
		}
	}
	return best, nil
}

func lessBitOrder(left, right []bool) bool {
	for index := range left {
		if left[index] != right[index] {
			return !left[index]
		}
	}
	return false
}
