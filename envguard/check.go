package envguard

// ViolationKind distinguishes the three independent export constraints.
type ViolationKind int

const (
	ViolationContent ViolationKind = iota
	ViolationControl
	ViolationPath
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationContent:
		return "content-origin"
	case ViolationControl:
		return "control-origin"
	default:
		return "path-prefix"
	}
}

// Violation is the earliest output interval that fails one constraint.
type Violation struct {
	Kind ViolationKind
	// From, To are half-open rune offsets in the un-normalized output.
	From int64
	To   int64
	// Origins backing the offending characters/dependencies.
	Origins []*Origin
}

// explainRuneCap bounds how much of an offending run is reported, keeping
// error messages small even for 2^44-length ropes.
const explainRuneCap = int64(64)

func (v Violation) explain(g *Graph, id NodeID) string {
	end := v.To
	if end-v.From > explainRuneCap {
		end = v.From + explainRuneCap
	}
	runes := make([]rune, 0, end-v.From)
	for i := v.From; i < end; i++ {
		runes = append(runes, g.RuneAt(id, i))
	}
	return string(runes)
}

// checkContent walks the rope and returns the first run whose character
// content originates outside the trusted origins.
func (g *Graph) checkContent(id NodeID) *Violation {
	n := g.nodes[id]
	if !n.content.anyOutside(g.reg) {
		return nil
	}
	pos := g.firstBadContent(id, 0)
	if pos < 0 {
		return nil
	}
	end := pos + 1
	for end < n.len {
		if g.firstBadContent(id, end) != end {
			break
		}
		end++
		if end-pos > explainRuneCap {
			break
		}
	}
	info, err := g.ProvenanceAt(id, pos)
	if err != nil {
		panic(err)
	}
	return &Violation{Kind: ViolationContent, From: pos, To: end, Origins: info.Content}
}

// firstBadContent returns the first offset >= start with untrusted content,
// or -1. Remove-type sanitizers are treated as trusted content at export:
// the host declared the exact deletion relation, so every surviving rune is
// vouched for.
func (g *Graph) firstBadContent(id NodeID, start int64) int64 {
	n := g.nodes[id]
	switch n.kind {
	case nLiteral:
		return -1
	case nVar:
		if n.content.anyOutside(g.reg) {
			return start
		}
		return -1
	case nSanitize:
		// Both relation kinds produce host-vouched output content.
		return -1
	case nConcat:
		var off int64
		for _, p := range n.parts {
			pl := g.nodes[p].len
			if start < off+pl {
				if got := g.firstBadContent(p, max64(0, start-off)); got >= 0 {
					return off + got
				}
			}
			off += pl
		}
		return -1
	case nLoop:
		bl := g.nodes[n.body].len
		if bl == 0 {
			return -1
		}
		idx := start / bl
		if got := g.firstBadContent(n.body, start-idx*bl); got >= 0 {
			return idx*bl + got
		}
		rest := (idx + 1) * bl
		if rest < n.len {
			return g.firstBadContent(id, rest)
		}
		return -1
	case nCond:
		ch := g.chosenBranch(n)
		return g.firstBadContent(ch, start)
	case nFold, nSlice:
		if n.kind == nSlice {
			got := g.firstBadContent(n.child, n.lo+start)
			if got < 0 {
				return -1
			}
			return got - n.lo
		}
		return g.firstBadContent(n.child, start)
	}
	return -1
}

// checkControl returns the first rune whose presence at its position depends
// on an untrusted origin through data shape or branch selection.
func (g *Graph) checkControl(id NodeID) *Violation {
	n := g.nodes[id]
	if !n.control.anyOutside(g.reg) {
		return nil
	}
	pos := g.firstBadControl(id, 0)
	if pos < 0 {
		return nil
	}
	end := pos + 1
	for end < n.len && g.firstBadControl(id, end) == end && end-pos < explainRuneCap {
		end++
	}
	info, err := g.ProvenanceAt(id, pos)
	if err != nil {
		panic(err)
	}
	return &Violation{Kind: ViolationControl, From: pos, To: end, Origins: info.Control}
}

func (g *Graph) firstBadControl(id NodeID, start int64) int64 {
	n := g.nodes[id]
	switch n.kind {
	case nLiteral:
		return -1
	case nVar:
		if n.control.anyOutside(g.reg) {
			return start
		}
		return -1
	case nSanitize:
		if !n.control.anyOutside(g.reg) {
			return -1
		}
		if n.san.Fixed != nil {
			// Only branch control survives; it applies to every output rune.
			return start
		}
		// Deletion keeps full shape control: locate the first surviving rune
		// whose underlying position is bad.
		in, err := g.mapRemove(n, start)
		if err != nil {
			return -1
		}
		got := g.firstBadControl(n.child, in)
		if got < 0 {
			return -1
		}
		return g.removeOutOffset(n, got)
	case nConcat:
		var off int64
		for _, p := range n.parts {
			pl := g.nodes[p].len
			if start < off+pl {
				if got := g.firstBadControl(p, max64(0, start-off)); got >= 0 {
					return off + got
				}
			}
			off += pl
		}
		return -1
	case nLoop:
		bl := g.nodes[n.body].len
		if bl == 0 {
			return -1
		}
		idx := start / bl
		if got := g.firstBadControl(n.body, start-idx*bl); got >= 0 {
			return idx*bl + got
		}
		rest := (idx + 1) * bl
		if rest < n.len {
			return g.firstBadControl(id, rest)
		}
		return -1
	case nCond:
		return g.condFirstBad(n, start, g.firstBadControl)
	case nSlice:
		got := g.firstBadControl(n.child, n.lo+start)
		if got < 0 {
			return -1
		}
		return got - n.lo
	case nFold:
		return g.firstBadControl(n.child, start)
	}
	return -1
}

// condFirstBad finds the earliest offset at which the two branches differ
// (content or length) and whose selector is untrusted. Runes that are
// identical on both branches do not depend on the selector, even if an
// untrusted condition selected one of them.
func (g *Graph) condFirstBad(n *node, start int64, recurse func(NodeID, int64) int64) int64 {
	a, b := n.child, n.childB
	la, lb := g.nodes[a].len, g.nodes[b].len
	common := la
	if lb < common {
		common = lb
	}
	selUntrusted := n.branchCt.anyOutside(g.reg)
	for i := start; i < common; i++ {
		if selUntrusted && g.RuneAt(a, i) != g.RuneAt(b, i) {
			return i
		}
	}
	if selUntrusted && n.len > common {
		if start < common {
			return common
		}
		return start
	}
	chosen := g.chosenBranch(n)
	if start < g.nodes[chosen].len {
		if got := recurse(chosen, start); got >= 0 {
			return got
		}
	}
	return -1
}

// removeOutOffset maps a child input offset to the sanitizer output offset
// by scanning forward from the nearest checkpoint.
func (g *Graph) removeOutOffset(n *node, in int64) int64 {
	idx := len(n.blocks) - 1
	for k, blk := range n.blocks {
		if blk.inStart > in {
			idx = k - 1
			break
		}
	}
	blk := n.blocks[idx]
	out := blk.outStart
	for pos := blk.inStart; pos < in; pos++ {
		if !n.san.removes(g.RuneAt(n.child, pos)) {
			out++
		}
	}
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
