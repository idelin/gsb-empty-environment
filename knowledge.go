package netsec

import (
	"fmt"
	"math"
	"sort"
)

// NoEvent marks a term that the attacker cannot derive from the facts
// observed so far.
const NoEvent int = math.MaxInt

// Rule identifies one of the closure rules (or the attacker observation
// rule that injects a base fact).
type Rule int

const (
	// RuleFact marks a term as directly observed at some event.
	RuleFact Rule = iota
	// RuleFst is pair projection: pair(x,y) -> x.
	RuleFst
	// RuleSnd is pair projection: pair(x,y) -> y.
	RuleSnd
	// RuleDec is symmetric decryption: enc(x,k), k -> x.
	RuleDec
	// RulePair is pairing: x, y -> pair(x,y).
	RulePair
	// RuleEnc is symmetric encryption: x, k -> enc(x,k).
	RuleEnc
)

func (r Rule) String() string {
	switch r {
	case RuleFact:
		return "fact"
	case RuleFst:
		return "fst"
	case RuleSnd:
		return "snd"
	case RuleDec:
		return "dec"
	case RulePair:
		return "pair"
	case RuleEnc:
		return "enc"
	default:
		return fmt.Sprintf("rule(%d)", int(r))
	}
}

// TermID is the compact, stable identifier of a hash-consed term known to
// an Engine. IDs are assigned in first-use order and remain valid for the
// lifetime of the engine.
type TermID int

// Edge is one hyperedge of the derivation DAG: the head term is derived
// from the dependency terms using Rule. A RuleFact edge has no
// dependencies.
type Edge struct {
	Head TermID
	Rule Rule
	Deps []TermID
}

// Fact is an attacker observation: the term became available at event
// AtEvent for the first time from the protocol trace.
type Fact struct {
	Term    Term `json:"term"`
	AtEvent int  `json:"at_event"`
}

// edgeRec extends Edge with the fixed tie-break rank. There is at most one
// edge per (head, rule), so rule order alone makes every selection
// deterministic.
type edgeRec struct {
	head TermID
	rule Rule
	deps []TermID
	rank int
}

// Engine maintains the incremental attacker knowledge closure. It is not
// safe for concurrent use; a symbol-verification host should drive it from
// a single goroutine or serialize calls externally.
type Engine struct {
	terms []termRec
	index map[string]TermID

	universe []bool
	earliest []int
	bestEdge []int
	edges    []edgeRec
	incoming [][]int

	factAt map[TermID]int
	facts  []Fact

	targets   []TermID
	targetSet map[TermID]bool

	pending []TermID
	queued  []bool

	autoVerify bool
	lastCheck  CheckResult
}

// Option configures an Engine at construction time.
type Option func(*Engine)

// WithAutoVerify toggles a full recomputation and consistency check after
// every mutation. It is enabled by default.
func WithAutoVerify(enabled bool) Option {
	return func(e *Engine) { e.autoVerify = enabled }
}

// NewEngine creates an empty closure engine.
func NewEngine(options ...Option) *Engine {
	e := &Engine{
		index:      make(map[string]TermID),
		factAt:     make(map[TermID]int),
		targetSet:  make(map[TermID]bool),
		autoVerify: true,
	}
	for _, option := range options {
		option(e)
	}
	return e
}

// Intern registers a term (and all of its subterms) and returns its
// identifier, creating the identifier on first use.
func (e *Engine) Intern(t Term) (TermID, error) {
	if err := validateTerm(t); err != nil {
		return 0, err
	}
	return e.intern(t), nil
}

// MustIntern is like Intern but panics on an invalid term.
func (e *Engine) MustIntern(t Term) TermID {
	id, err := e.Intern(t)
	if err != nil {
		panic(err)
	}
	return id
}

// ParseIntern parses and registers a term.
func (e *Engine) ParseIntern(s string) (TermID, error) {
	t, err := ParseTerm(s)
	if err != nil {
		return 0, err
	}
	return e.intern(t), nil
}

func (e *Engine) intern(t Term) TermID {
	if id, ok := e.index[t.String()]; ok {
		return id
	}
	children := make([]TermID, len(t.Children))
	for i, child := range t.Children {
		children[i] = e.intern(child)
	}

	id := TermID(len(e.terms))
	e.terms = append(e.terms, termRec{term: t, canonical: t.String()})
	e.index[t.String()] = id
	e.universe = append(e.universe, false)
	e.earliest = append(e.earliest, NoEvent)
	e.bestEdge = append(e.bestEdge, -1)
	e.incoming = append(e.incoming, nil)
	e.queued = append(e.queued, false)

	e.activate(id, children)
	return id
}

// activate moves a term into the finite derivation universe and creates
// every closure edge having this term as its head. Children are already
// active because intern registers terms bottom-up.
func (e *Engine) activate(id TermID, children []TermID) {
	if e.universe[id] {
		return
	}
	e.universe[id] = true

	rec := &e.terms[id]
	switch rec.term.Kind {
	case Pair:
		e.addEdge(edgeRec{head: children[0], rule: RuleFst, deps: []TermID{id}, rank: int(RuleFst)})
		e.addEdge(edgeRec{head: children[1], rule: RuleSnd, deps: []TermID{id}, rank: int(RuleSnd)})
		e.addEdge(edgeRec{head: id, rule: RulePair, deps: append([]TermID(nil), children...), rank: int(RulePair)})
	case Enc:
		e.addEdge(edgeRec{head: children[0], rule: RuleDec, deps: []TermID{id, children[1]}, rank: int(RuleDec)})
		e.addEdge(edgeRec{head: id, rule: RuleEnc, deps: append([]TermID(nil), children...), rank: int(RuleEnc)})
	}

	if _, ok := e.factAt[id]; ok {
		e.addEdge(edgeRec{head: id, rule: RuleFact, rank: int(RuleFact)})
	}
	e.enqueue(id)
}

func (e *Engine) addEdge(edge edgeRec) {
	idx := len(e.edges)
	e.edges = append(e.edges, edge)
	for _, dep := range edge.deps {
		e.incoming[dep] = append(e.incoming[dep], idx)
	}
}

// AddFact records that the attacker observes term t at event atEvent.
// Events are numbered starting at 1. The same term observed again at an
// earlier event tightens its first-availability time; a later duplicate is
// retained in the history but changes nothing.
func (e *Engine) AddFact(t Term, atEvent int) error {
	if atEvent < 1 {
		return fmt.Errorf("netsec: event numbers must be positive, got %d", atEvent)
	}
	if err := validateTerm(t); err != nil {
		return err
	}
	id := e.intern(t)

	previous, known := e.factAt[id]
	if !known {
		e.factAt[id] = atEvent
		e.facts = append(e.facts, Fact{Term: t, AtEvent: atEvent})
		e.addEdge(edgeRec{head: id, rule: RuleFact, rank: int(RuleFact)})
		e.enqueue(id)
	} else {
		e.facts = append(e.facts, Fact{Term: t, AtEvent: atEvent})
		if atEvent < previous {
			e.factAt[id] = atEvent
			e.enqueue(id)
		}
	}

	e.drain()
	if e.autoVerify {
		check := e.Check()
		e.lastCheck = check
		if !check.OK {
			return fmt.Errorf("netsec: internal consistency failure after fact %s@%d: %s", t, atEvent, check.Reason)
		}
	}
	return nil
}

// AddFactString parses s and records it as an observed fact.
func (e *Engine) AddFactString(s string, atEvent int) error {
	t, err := ParseTerm(s)
	if err != nil {
		return err
	}
	return e.AddFact(t, atEvent)
}

// SetTarget registers a secret whose earliest knowability must be tracked.
func (e *Engine) SetTarget(t Term) (TermID, error) {
	id, err := e.Intern(t)
	if err != nil {
		return 0, err
	}
	if !e.targetSet[id] {
		e.targetSet[id] = true
		e.targets = append(e.targets, id)
		sort.Slice(e.targets, func(i, j int) bool { return e.targets[i] < e.targets[j] })
	}
	e.drain()
	return id, nil
}

func (e *Engine) enqueue(id TermID) {
	if !e.queued[id] {
		e.queued[id] = true
		e.pending = append(e.pending, id)
	}
}

// drain runs the semi-naive fixpoint: each re-evaluation reads only the
// current earliest times of its dependencies and takes their maximum.
func (e *Engine) drain() {
	for len(e.pending) > 0 {
		id := e.pending[0]
		e.pending = e.pending[1:]
		e.queued[id] = false
		e.evaluate(id)
	}
}

func (e *Engine) evaluate(id TermID) {
	bestTime := NoEvent
	bestEdge := -1
	for _, edgeIdx := range e.edgesOf(id) {
		edge := &e.edges[edgeIdx]
		candidate, ok := e.edgeTime(edge)
		if !ok || e.edgeWouldCycle(id, edge) {
			continue
		}
		if candidate < bestTime || (candidate == bestTime && (bestEdge == -1 || edge.rank < e.edges[bestEdge].rank)) {
			bestTime = candidate
			bestEdge = edgeIdx
		}
	}

	if bestTime != e.earliest[id] || bestEdge != e.bestEdge[id] {
		e.earliest[id] = bestTime
		e.bestEdge[id] = bestEdge
		for _, edgeIdx := range e.incoming[id] {
			e.enqueue(e.edges[edgeIdx].head)
		}
	}
}

// edgeWouldCycle rejects edges whose selected-premise DAG already reaches
// the head. Knowledge derivations form a DAG over term identities: an
// attacker cannot construct a term using itself as a premise.
func (e *Engine) edgeWouldCycle(head TermID, edge *edgeRec) bool {
	for _, dep := range edge.deps {
		if e.reaches(dep, head, make(map[TermID]bool)) {
			return true
		}
	}
	return false
}

func (e *Engine) reaches(from, target TermID, seen map[TermID]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	idx := e.bestEdge[from]
	if idx < 0 {
		return false
	}
	for _, dep := range e.edges[idx].deps {
		if e.reaches(dep, target, seen) {
			return true
		}
	}
	return false
}

func (e *Engine) edgesOf(id TermID) []int {
	var result []int
	for idx := range e.edges {
		if e.edges[idx].head == id {
			result = append(result, idx)
		}
	}
	return result
}

// edgeTime returns the earliest event at which the edge can fire, namely
// the maximum first-availability event of every premise. The observation
// edge fires at the fact event itself.
func (e *Engine) edgeTime(edge *edgeRec) (int, bool) {
	if edge.rule == RuleFact {
		at, ok := e.factAt[edge.head]
		return at, ok
	}
	latest := 0
	for _, dep := range edge.deps {
		at := e.earliest[dep]
		if at == NoEvent {
			return 0, false
		}
		if at > latest {
			latest = at
		}
	}
	return latest, true
}

// Term resolves an identifier back to its value representation.
func (e *Engine) Term(id TermID) Term {
	return e.terms[id].term
}

// TermString renders an identifier as canonical syntax.
func (e *Engine) TermString(id TermID) string {
	return e.terms[id].canonical
}

// Lookup finds an already-registered term.
func (e *Engine) Lookup(t Term) (TermID, bool) {
	id, ok := e.index[t.String()]
	return id, ok
}

// Earliest reports the first event at which the attacker can know id and
// whether it is knowable at all.
func (e *Engine) Earliest(id TermID) (atEvent int, known bool) {
	at := e.earliest[id]
	if at == NoEvent {
		return NoEvent, false
	}
	return at, true
}

// Derivation returns the selected edge that first derives id, or false if
// id is not knowable.
func (e *Engine) Derivation(id TermID) (Edge, bool) {
	idx := e.bestEdge[id]
	if idx < 0 {
		return Edge{}, false
	}
	edge := e.edges[idx]
	return Edge{Head: edge.head, Rule: edge.rule, Deps: append([]TermID(nil), edge.deps...)}, true
}

// Facts returns the observed facts in arrival order.
func (e *Engine) Facts() []Fact {
	return append([]Fact(nil), e.facts...)
}

// Targets returns registered target identifiers in registration-stable
// sorted order.
func (e *Engine) Targets() []TermID {
	return append([]TermID(nil), e.targets...)
}

// KnownTerm is one entry of the current attacker knowledge.
type KnownTerm struct {
	ID      TermID `json:"id"`
	Term    Term   `json:"term"`
	AtEvent int    `json:"at_event"`
}

// Known returns every term currently derivable by the attacker, ordered by
// (availability event, identifier).
func (e *Engine) Known() []KnownTerm {
	var result []KnownTerm
	for id := range e.terms {
		if e.earliest[id] != NoEvent {
			result = append(result, KnownTerm{
				ID:      TermID(id),
				Term:    e.terms[id].term,
				AtEvent: e.earliest[id],
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AtEvent != result[j].AtEvent {
			return result[i].AtEvent < result[j].AtEvent
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// LastCheck returns the most recent automatic consistency check result.
func (e *Engine) LastCheck() CheckResult {
	return e.lastCheck
}
