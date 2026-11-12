package netsec

import (
	"fmt"
	"sort"
)

// Default enumeration limits. The graph is finite but cyclic, so concrete
// path enumeration is bounded; reaching a limit is reported rather than
// hidden.
const (
	defaultMaxPaths       = 10000
	defaultMaxBlockVisits = 32
	defaultMaxDepth       = 256
)

// Limits bounds concrete path enumeration. Zero fields fall back to defaults.
type Limits struct {
	// MaxPaths is the maximum number of terminal paths reported.
	MaxPaths int
	// MaxBlockVisits is how many times one block may appear on a single
	// path. It bounds loop unrolling.
	MaxBlockVisits int
	// MaxDepth is the longest allowed block sequence.
	MaxDepth int
}

// Analyze enumerates concrete paths through g and returns the effect report.
func Analyze(g *CFG, limits Limits) (*Report, error) {
	if err := g.validate(); err != nil {
		return nil, err
	}
	applyDefaultLimits(&limits)

	analysis := &analysis{
		g:       g,
		limits:  limits,
		preds:   predecessors(g),
		out:     outgoing(g),
		visited: map[string]int{},
	}
	analysis.run()
	return analysis.report, nil
}

type analysis struct {
	g      *CFG
	limits Limits
	preds  map[string][]Edge
	out    map[string][]Edge

	visited         map[string]int
	blocks          []string
	edges           []EdgeKind
	everException   bool
	exceptionMerges map[string]bool
	report          *Report
}

func applyDefaultLimits(l *Limits) {
	if l.MaxPaths <= 0 {
		l.MaxPaths = defaultMaxPaths
	}
	if l.MaxBlockVisits <= 0 {
		l.MaxBlockVisits = defaultMaxBlockVisits
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = defaultMaxDepth
	}
}

func predecessors(g *CFG) map[string][]Edge {
	preds := map[string][]Edge{}
	for _, edge := range g.Edges {
		preds[edge.To] = append(preds[edge.To], edge)
	}
	return preds
}

func outgoing(g *CFG) map[string][]Edge {
	out := map[string][]Edge{}
	for _, edge := range g.Edges {
		out[edge.From] = append(out[edge.From], edge)
	}
	// Deterministic exploration order.
	for from := range out {
		sort.Slice(out[from], func(i, j int) bool {
			if out[from][i].To != out[from][j].To {
				return out[from][i].To < out[from][j].To
			}
			return out[from][i].Kind < out[from][j].Kind
		})
	}
	return out
}

func (a *analysis) run() {
	a.exceptionMerges = map[string]bool{}
	a.report = &Report{Merges: a.computeMerges()}
	a.enter(a.g.Entry, EdgeNormal, newEffectState())
	for i := range a.report.Merges {
		a.report.Merges[i].ReachableViaException = a.exceptionMerges[a.report.Merges[i].Block]
	}
	a.aggregate()
}

func (a *analysis) computeMerges() []MergeInfo {
	var infos []MergeInfo
	for id := range a.g.Blocks {
		in := a.preds[id]
		if len(in) <= 1 {
			continue
		}
		info := MergeInfo{Block: id, Predecessors: len(in)}
		for _, edge := range in {
			if edge.Kind == EdgeException {
				info.HasExceptionIn = true
			} else {
				info.HasNormalIn = true
			}
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Block < infos[j].Block })
	return infos
}

// enter processes one block arrival. inEdge is the edge used to reach the
// block (EdgeNormal for the entry). The state already reflects the edge's
// transfer effect.
func (a *analysis) enter(blockID string, inEdge EdgeKind, state *effectState) {
	prevException := a.everException
	if inEdge == EdgeException {
		a.everException = true
	}
	defer func() { a.everException = prevException }()

	if len(a.report.Paths) >= a.limits.MaxPaths {
		a.cutOff(blockID, state, "max paths reached")
		return
	}
	if len(a.blocks) >= a.limits.MaxDepth {
		a.cutOff(blockID, state, "max depth reached")
		return
	}
	if a.visited[blockID] >= a.limits.MaxBlockVisits {
		a.cutOff(blockID, state, "max block visits reached")
		return
	}

	a.visited[blockID]++
	a.blocks = append(a.blocks, blockID)
	if len(a.blocks) > 1 {
		a.edges = append(a.edges, inEdge)
	}
	defer func() {
		a.visited[blockID]--
		a.blocks = a.blocks[:len(a.blocks)-1]
		if len(a.edges) > 0 {
			a.edges = a.edges[:len(a.edges)-1]
		}
	}()

	block := a.g.Blocks[blockID]

	// Confluence: exceptional and normal flows can join at this block. Each
	// concrete path keeps its own state, so merging edges never erases a
	// failure carried on one of them.
	if len(a.preds[blockID]) > 1 {
		if a.everException {
			a.exceptionMerges[blockID] = true
		}
		state.record(EventMerge, blockID, "",
			fmt.Sprintf("joined %d incoming flows", len(a.preds[blockID])))
	}

	// A recover block is reached through an exception edge; consume the
	// panic before executing any remaining operations.
	startOp := 0
	if len(block.Ops) > 0 {
		if rec, ok := block.Ops[0].(OpRecover); ok {
			state.applyRecover(blockID, rec)
			startOp = 1
		}
	}

	for i := startOp; i < len(block.Ops); i++ {
		switch op := block.Ops[i].(type) {
		case OpAuth:
			// Outcome is decided by the outgoing edge; nothing to do here.
		case OpErrorAssign:
			state.applyErrorAssign(blockID, op)
		case OpPanic:
			state.applyPanic(blockID, op)
		case OpProtectedCallback:
			state.applyProtectedCallback(blockID, op)
		case OpReturn:
			terminal := state.applyReturn(blockID, op)
			a.finalize(state, terminal)
			return
		}
	}

	successors := a.out[blockID]
	if len(successors) == 0 {
		state.record(EventImplicitEnd, blockID, "", "block without outgoing edge")
		a.finalize(state, Terminal{
			Kind:            TerminalImplicit,
			Block:           blockID,
			FailureSurfaced: state.failed && !state.masked,
		})
		return
	}

	for _, edge := range successors {
		next := state.clone()
		a.applyEdgeTransfer(block, edge, next)
		a.enter(edge.To, edge.Kind, next)
	}
}

// applyEdgeTransfer models the effect of leaving a block along an edge.
func (a *analysis) applyEdgeTransfer(block *Block, edge Edge, state *effectState) {
	if len(block.Ops) == 0 {
		return
	}
	switch op := block.Ops[len(block.Ops)-1].(type) {
	case OpAuth:
		switch edge.Kind {
		case EdgeNormal:
			state.applyAuthSuccess(block.ID, op)
		default:
			if op.ErrVar == "" {
				state.applyAuthPanic(block.ID, op)
			} else {
				state.applyAuthFailure(block.ID, op)
			}
		}
	case OpPanic:
		// The panic stays in flight until an OpRecover consumes it.
		state.panicInFlight = true
		state.panicSite = op.Site
	}
}

func (a *analysis) cutOff(blockID string, state *effectState, reason string) {
	a.report.Truncated = true
	a.report.LimitReason = reason
	a.finalize(state, Terminal{Kind: TerminalCutOff, Block: blockID})
}

func (a *analysis) finalize(state *effectState, terminal Terminal) {
	index := len(a.report.Paths)
	path := PathResult{
		Index:     index,
		Blocks:    append([]string(nil), a.blocks...),
		EdgeKinds: append([]EdgeKind(nil), a.edges...),
		Events:    append([]PathEvent(nil), state.events...),
		Terminal:  terminal,
	}
	for _, finding := range state.findings {
		finding.PathIndex = index
		path.Findings = append(path.Findings, finding)
	}
	a.report.Paths = append(a.report.Paths, path)
}

func (a *analysis) aggregate() {
	type key struct {
		kind  FindingKind
		block string
		site  string
	}
	order := []key{}
	counts := map[key]int{}
	details := map[key]string{}

	for _, path := range a.report.Paths {
		for _, finding := range path.Findings {
			k := key{finding.Kind, finding.Block, finding.Site}
			if _, seen := counts[k]; !seen {
				order = append(order, k)
				details[k] = finding.Detail
			}
			counts[k]++
		}
	}

	for _, k := range order {
		a.report.Findings = append(a.report.Findings, Finding{
			Kind:   k.kind,
			Block:  k.block,
			Site:   k.site,
			Count:  counts[k],
			Detail: details[k],
		})
	}
}
