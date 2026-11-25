package capsum

import (
	"sort"

	"prescreen.local/ps-2237843e01/pathlang"
)

// Status classifies the analysis outcome for a program point.
type Status string

const (
	StatusReachable   Status = "reachable"
	StatusUnreachable Status = "unreachable"
	StatusUnknown     Status = "unknown"
)

// frameState is the abstract permission pair at one (node, queue-mask) context.
// S summarizes the synchronous-stack restriction and F every restriction
// carried by an enclosing asynchronous task. The effective language is S ∩ F.
type frameState struct {
	s dnf
	f dnf
}

func effective(st frameState) dnf { return intersectDNF(st.s, st.f) }

func joinState(a, b frameState) frameState {
	return frameState{s: unionDNF(a.s, b.s), f: unionDNF(a.f, b.f)}
}

// PointResult is the settled summary for one program point.
type PointResult struct {
	Node      string
	Status    Status
	May       *pathlang.DFA
	Must      *pathlang.DFA
	ReachedBy []uint16
}

// HandleSummary records the frozen language accumulated at a handle.
type HandleSummary struct {
	Name    string
	May     *pathlang.DFA
	Must    *pathlang.DFA
	Present bool
}

// GeneratorRecord exposes one canonical grant-atom generator and its DFA.
type GeneratorRecord struct {
	ID    int
	Grant Grant
	DFA   *pathlang.DFA
}

// Result is the full analysis output.
type Result struct {
	Program          string
	Alphabet         []string
	Components       []string
	Points           map[string]*PointResult
	Handles          map[string]*HandleSummary
	BudgetExhausted  bool
	Iterations       int
	Budget           int
	GeneratorRecords []GeneratorRecord
}

// stateKey identifies an abstract configuration: a node, the set of pending
// queue handles (mask), and the entry label under which the enclosing method
// was invoked. Labels keep distinct calling contexts separate so that stack
// frames intersect rather than merge.
type stateKey struct {
	node  string
	mask  uint16
	label int
}

// label is one distinct method-entry context. The entry method uses label 0;
// every call/consume edge allocates labels lazily for its target.
type label struct {
	// entryS/entryF are the symbolic DNFs the method starts with. For a call
	// they derive from the caller context; for a task entry F carries the
	// frozen ∩ consumer restriction and S is the universal language.
	entryS dnf
	entryF dnf
}

type analysis struct {
	x      *index
	budget int

	states  map[stateKey]frameState
	queue   []stateKey
	queued  map[stateKey]bool
	handles map[int]dnf
	parked  map[int][]returnTarget
	labels  []label
	// edgeLabels maps a call/consume edge index to the labels it has spawned.
	edgeLabels map[int][]int

	iterations      int
	budgetExhausted bool
}

// Analyze runs the fixpoint analysis. budget <= 0 means unlimited.
func Analyze(p *Program, budget int) (*Result, error) {
	a, err := analyzeInternal(p, budget)
	if err != nil {
		return nil, err
	}
	return a.buildResult(), nil
}

// AnalyzeWithEvidence runs the analysis and also returns the internal analysis
// so certificates can include fixpoint evidence.
func AnalyzeWithEvidence(p *Program, budget int) (*Result, *analysis, error) {
	a, err := analyzeInternal(p, budget)
	if err != nil {
		return nil, nil, err
	}
	return a.buildResult(), a, nil
}

func analyzeInternal(p *Program, budget int) (*analysis, error) {
	x, err := Validate(p)
	if err != nil {
		return nil, err
	}
	a := &analysis{
		x:          x,
		budget:     budget,
		states:     map[stateKey]frameState{},
		queued:     map[stateKey]bool{},
		handles:    map[int]dnf{},
		parked:     map[int][]returnTarget{},
		labels:     []label{{}},
		edgeLabels: map[int][]int{},
	}

	a.labels[0] = label{
		entryS: dnfFromGenerators(x.domainUnion(p.EntryDomains)),
		entryF: universalDNF(),
	}
	a.put(stateKey{node: p.EntryNode, mask: 0, label: 0},
		frameState{s: a.labels[0].entryS, f: a.labels[0].entryF})

	for len(a.queue) > 0 {
		if budget > 0 && a.iterations >= budget {
			a.budgetExhausted = true
			break
		}
		k := a.queue[0]
		a.queue = a.queue[1:]
		a.queued[k] = false
		a.iterations++
		a.process(k, a.states[k])
	}

	return a, nil
}

func (a *analysis) put(k stateKey, st frameState) {
	old, ok := a.states[k]
	if !ok {
		a.states[k] = st
	} else {
		merged := joinState(old, st)
		if merged.s.equal(old.s) && merged.f.equal(old.f) {
			return
		}
		a.states[k] = merged
	}
	// Growth at a node can create new callee/task exits, so re-feed every
	// invocation edge entering it. This is the mechanism by which recursive
	// summaries converge without an artificial stack-depth bound.
	a.releaseExitsFor(k.node)
	if !a.queued[k] {
		a.queued[k] = true
		a.queue = append(a.queue, k)
	}
}

func sortMasks(m []uint16) { sort.Slice(m, func(i, j int) bool { return m[i] < m[j] }) }
