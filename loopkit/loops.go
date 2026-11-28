package loopkit

// Loop describes one natural loop rooted at a unique header.
type Loop struct {
	Header      *Block
	BackEdges   []*Block // latch blocks branching back to Header
	Blocks      map[*Block]bool
	Parent      *Loop
	Children    []*Loop
	Depth       int
	Preheader   *Block
	Irreducible bool // body intersects an irreducible SCC region
	Forest      *LoopForest
}

// Contains reports whether b belongs to the loop body.
func (l *Loop) Contains(b *Block) bool { return l.Blocks[b] }

// Latches returns the back-edge source blocks.
func (l *Loop) Latches() []*Block { return l.BackEdges }

// ExitingBlocks returns body blocks with an edge leaving the loop.
func (l *Loop) ExitingBlocks() []*Block {
	var out []*Block
	for b := range l.Blocks {
		for _, s := range b.Succs() {
			if !l.Blocks[s] {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// ExitBlocks returns outside-loop blocks targeted by loop exit edges.
func (l *Loop) ExitBlocks() []*Block {
	seen := map[*Block]bool{}
	var out []*Block
	for b := range l.Blocks {
		for _, s := range b.Succs() {
			if !l.Blocks[s] && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// SCC is a strongly connected component of the CFG.
type SCC struct {
	Nodes       []*Block
	Irreducible bool
}

// LoopForest bundles all loop and SCC analysis results for a function.
type LoopForest struct {
	Func              *Func
	Dom               *DomTree
	Loops             []*Loop // all natural loops, outer to inner order
	TopLoops          []*Loop
	SCCs              []*SCC
	irreducibleBlocks map[*Block]bool
}

// IsIrreducibleBlock reports whether b sits inside an irreducible SCC
// (a cyclic region with two or more non-dominating entry points).
func (f *LoopForest) IsIrreducibleBlock(b *Block) bool {
	return f.irreducibleBlocks[b]
}

// IrreducibleSCCs returns only the irreducible components.
func (f *LoopForest) IrreducibleSCCs() []*SCC {
	var out []*SCC
	for _, s := range f.SCCs {
		if s.Irreducible {
			out = append(out, s)
		}
	}
	return out
}

// AnalyzeLoops computes the dominator tree, finds back edges and
// natural loops, builds the loop nesting forest, and separately runs
// Tarjan SCC to flag irreducible regions. It never mutates the CFG.
func AnalyzeLoops(f *Func) *LoopForest {
	dom := ComputeDomTree(f)
	forest := &LoopForest{
		Func:              f,
		Dom:               dom,
		irreducibleBlocks: map[*Block]bool{},
	}
	forest.SCCs = tarjanSCC(f, dom)

	// Group back edges u -> h where h dominates u, keyed by header.
	byHeader := map[*Block][]*Block{}
	for _, u := range f.Blocks {
		for _, h := range u.Succs() {
			if dom.Dominates(h, u) {
				byHeader[h] = append(byHeader[h], u)
			}
		}
	}
	for h, latches := range byHeader {
		forest.Loops = append(forest.Loops, buildNaturalLoop(h, latches, forest))
	}

	// Irreducible SCCs never get treated as natural loops even when
	// some internal edge happens to look dominator-related.
	for _, scc := range forest.SCCs {
		if !scc.Irreducible {
			continue
		}
		for _, n := range scc.Nodes {
			forest.irreducibleBlocks[n] = true
		}
	}
	for _, l := range forest.Loops {
		for b := range l.Blocks {
			if forest.irreducibleBlocks[b] {
				l.Irreducible = true
				break
			}
		}
	}

	forest.buildNesting()
	return forest
}

func buildNaturalLoop(header *Block, latches []*Block, forest *LoopForest) *Loop {
	l := &Loop{
		Header: header,
		Blocks: map[*Block]bool{header: true},
		Forest: forest,
	}
	work := []*Block{}
	push := func(b *Block) {
		if !l.Blocks[b] {
			l.Blocks[b] = true
			work = append(work, b)
		}
	}
	for _, latch := range latches {
		l.BackEdges = append(l.BackEdges, latch)
		push(latch)
	}
	for len(work) > 0 {
		x := work[len(work)-1]
		work = work[:len(work)-1]
		for _, p := range predsOf(x) {
			push(p)
		}
	}
	return l
}

func (f *LoopForest) buildNesting() {
	// Smaller loops first so each loop links to its innermost parent.
	bySize := append([]*Loop(nil), f.Loops...)
	for i := 0; i < len(bySize); i++ {
		for j := i + 1; j < len(bySize); j++ {
			if len(bySize[j].Blocks) < len(bySize[i].Blocks) {
				bySize[i], bySize[j] = bySize[j], bySize[i]
			}
		}
	}
	for _, l := range bySize {
		for _, cand := range bySize {
			if cand == l || !cand.Blocks[l.Header] {
				continue
			}
			if l.Parent == nil || len(cand.Blocks) < len(l.Parent.Blocks) {
				l.Parent = cand
			}
		}
		if l.Parent == nil {
			f.TopLoops = append(f.TopLoops, l)
		} else {
			l.Parent.Children = append(l.Parent.Children, l)
		}
	}
	f.Loops = nil
	var emit func(l *Loop)
	emit = func(l *Loop) {
		f.Loops = append(f.Loops, l)
		for _, c := range l.Children {
			emit(c)
		}
	}
	for _, t := range f.TopLoops {
		emit(t)
	}
	for _, l := range f.Loops {
		if l.Parent == nil {
			l.Depth = 1
		} else {
			l.Depth = l.Parent.Depth + 1
		}
	}
}

// LoopAt returns the innermost natural loop containing b, or nil.
func (f *LoopForest) LoopAt(b *Block) *Loop {
	var best *Loop
	for _, l := range f.Loops {
		if l.Blocks[b] && (best == nil || len(l.Blocks) < len(best.Blocks)) {
			best = l
		}
	}
	return best
}

// EnsurePreheaders guarantees every natural loop has exactly one
// preheader block. An existing unique preheader is recognized; otherwise
// a fresh block is inserted and phi edges are redirected correctly.
// The CFG is mutated, so loop analysis must be recomputed afterwards.
func (f *LoopForest) EnsurePreheaders() int {
	created := 0
	for _, l := range f.Loops {
		// A predecessor edge is a back edge exactly when the header
		// dominates it; everything else enters the loop from outside.
		back := map[*Block]bool{}
		for _, p := range predsOf(l.Header) {
			if f.Dom.Dominates(l.Header, p) {
				back[p] = true
			}
		}
		var outside []*Block
		for _, p := range predsOf(l.Header) {
			if !back[p] {
				outside = append(outside, p)
			}
		}
		if len(outside) == 1 {
			only := outside[0]
			if len(only.Succs()) == 1 && only.Succs()[0] == l.Header {
				l.Preheader = only
				continue
			}
		}
		ph := f.insertPreheader(l, outside)
		l.Preheader = ph
		created++
	}
	return created
}

func (f *LoopForest) insertPreheader(l *Loop, outside []*Block) *Block {
	fn := f.Func
	ph := fn.NewBlock(l.Header.Name + ".preheader")

	// Materialize one phi in the preheader per header phi, merging the
	// incoming values from every redirected outside predecessor.
	headerPhis := l.Header.Phis()
	mergePhis := make([]*Instr, len(headerPhis))
	for idx, hp := range headerPhis {
		var edges []PhiEdge
		for _, p := range outside {
			edges = append(edges, PhiEdge{From: p, Value: phiValueFor(hp, p)})
		}
		mergePhis[idx] = ph.EmitPhi(hp.Typ, edges)
	}
	ph.SetBr(l.Header)

	for _, p := range outside {
		p.Retarget(l.Header, ph)
	}
	for idx, hp := range headerPhis {
		// Drop all outside edges, keep latch edges, add preheader edge.
		var vals []*Instr
		var pbs []*Block
		for k, pb := range hp.PredBlocks {
			if !containsBlock(outside, pb) {
				vals = append(vals, hp.Operands[k])
				pbs = append(pbs, pb)
			}
		}
		vals = append(vals, mergePhis[idx])
		pbs = append(pbs, ph)
		hp.Operands, hp.PredBlocks = vals, pbs
	}

	// NewBlock already appended the preheader; block order is irrelevant
	// because RPO is recomputed on every analysis.
	return ph
}

func phiValueFor(phi *Instr, pred *Block) *Instr {
	for idx, pb := range phi.PredBlocks {
		if pb == pred {
			return phi.Operands[idx]
		}
	}
	return nil
}

func containsBlock(list []*Block, b *Block) bool {
	for _, x := range list {
		if x == b {
			return true
		}
	}
	return false
}

// tarjanSCC runs Tarjan's algorithm and tags cyclic SCCs having two or
// more entry points as irreducible.
func tarjanSCC(f *Func, dom *DomTree) []*SCC {
	index := map[*Block]int{}
	low := map[*Block]int{}
	onStack := map[*Block]bool{}
	var stack []*Block
	next := 0
	var sccs []*SCC

	var strong func(v *Block)
	strong = func(v *Block) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range v.Succs() {
			if _, ok := index[w]; !ok {
				strong(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var nodes []*Block
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				nodes = append(nodes, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, classifySCC(f, nodes, dom))
		}
	}
	for _, b := range f.Blocks {
		if _, ok := index[b]; !ok {
			strong(b)
		}
	}
	return sccs
}

func classifySCC(f *Func, nodes []*Block, dom *DomTree) *SCC {
	scc := &SCC{Nodes: nodes}
	member := map[*Block]bool{}
	for _, n := range nodes {
		member[n] = true
	}
	cyclic := false
	var headers []*Block
	for _, n := range nodes {
		for _, p := range predsOf(n) {
			if p == n || member[p] {
				cyclic = true
			}
			if !member[p] && !containsBlock(headers, n) {
				headers = append(headers, n)
			}
		}
	}
	// A cyclic region is irreducible when it has two or more entry
	// points and no single entry dominates every SCC node.
	if cyclic && len(headers) >= 2 {
		for _, h := range headers {
			domAll := true
			for _, n := range nodes {
				if !dom.Dominates(h, n) {
					domAll = false
					break
				}
			}
			if domAll {
				return scc // reducible: one entry rules the region
			}
		}
		scc.Irreducible = true
	}
	return scc
}
