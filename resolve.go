package destplan

import "sort"

type resolveState struct {
	pieces []Piece
	gaps   []Gap
}

// Resolve symbolically expands req within snap. The result keeps whole block
// ranges rather than enumerating sectors: a leaf covering one million sectors
// still yields a constant number of pieces.
func Resolve(snap Snapshot, req Interval) (*Resolution, error) {
	if snap.Root == nil {
		return nil, errf("snapshot %q has no root", snap.ID)
	}
	if req.Empty() {
		return &Resolution{}, nil
	}
	lo, hi := req.clampTo(snap.Root.Span)
	st := &resolveState{}
	if addrCmp(lo, hi) < 0 {
		st.resolveNode(snap.Root, lo, hi, snap.Shift)
	}
	st.merge()
	return &Resolution{Pieces: st.pieces, Gaps: st.gaps}, nil
}

// resolveNode handles local range [lo, hi) of node n, applying the snapshot
// shift so every produced logical coordinate is in snapshot space.
func (s *resolveState) resolveNode(n *MapNode, lo, hi Addr, shift int64) {
	switch n.Kind {
	case NodeHole:
		s.emitGap(n, lo, hi, shift, true)
	case NodeLeaf:
		s.emitLeaf(n, lo, hi, shift)
	case NodeShift:
		s.resolveShift(n, lo, hi, shift)
	case NodeBranch:
		s.resolveBranch(n, lo, hi, shift)
	default:
		panic("destplan: unknown node kind")
	}
}

func (s *resolveState) emitGap(n *MapNode, lo, hi Addr, shift int64, hole bool) {
	glo, ok1 := addrShift(lo, shift)
	ghi, ok2 := addrShift(hi, shift)
	if !ok1 || !ok2 {
		panic("destplan: shifted gap left the address space")
	}
	s.gaps = append(s.gaps, Gap{LogLo: glo, LogHi: ghi, Hole: hole})
}

func (s *resolveState) emitLeaf(n *MapNode, lo, hi Addr, shift int64) {
	blockStart, ok1 := addrAdd(Addr64(n.BlockBase), lo)
	blockEnd, ok2 := addrAdd(Addr64(n.BlockBase), hi)
	coordBase := n.CoordBase
	if coordBase == 0 {
		coordBase = n.BlockBase
	}
	coordStart, ok3 := addrAdd(Addr64(coordBase), lo)
	coordEnd, ok4 := addrAdd(Addr64(coordBase), hi)
	logLo, l1 := addrShift(lo, shift)
	logHi, l2 := addrShift(hi, shift)
	if !ok1 || !ok2 || !ok3 || !ok4 || !l1 || !l2 {
		panic("destplan: leaf coordinates left the address space")
	}
	if !blockStart.InSpace() || addrCmp(blockEnd, AddrEnd()) > 0 {
		panic("destplan: block identity left the 64-bit space")
	}
	if !coordStart.InSpace() || addrCmp(coordEnd, AddrEnd()) > 0 {
		panic("destplan: storage coordinate left the 64-bit space")
	}
	s.pieces = append(s.pieces, Piece{
		LogLo:   logLo,
		LogHi:   logHi,
		BlockLo: blockStart.Lo,
		BlockHi: blockEnd,
		CoordLo: coordStart.Lo,
		CoordHi: coordEnd,
		Key:     n.DataKey,
	})
}

func (s *resolveState) resolveShift(n *MapNode, lo, hi Addr, shift int64) {
	s.resolveNode(n.Inner, lo, hi, shift+n.Delta)
}

func (s *resolveState) resolveBranch(n *MapNode, lo, hi Addr, shift int64) {
	cursor := lo
	for _, ch := range n.Children {
		childStart := Addr64(ch.Offset)
		childEnd, ok := addrAdd(childStart, ch.Node.Span)
		if !ok || addrCmp(childEnd, lo) <= 0 {
			continue
		}
		if addrCmp(childStart, cursor) > 0 {
			s.emitGap(n, cursor, minAddr(childStart, hi), shift, true)
		}
		if addrCmp(childStart, hi) >= 0 {
			cursor = maxAddr(cursor, childStart)
			break
		}
		subLo := maxAddr(lo, childStart)
		subHi := minAddr(hi, childEnd)
		if addrCmp(subLo, subHi) < 0 {
			relLo, bok1 := addrSub(subLo, childStart)
			relHi, bok2 := addrSub(subHi, childStart)
			if !bok1 || !bok2 {
				panic("destplan: branch child offset underflow")
			}
			s.resolveNode(ch.Node, relLo, relHi, shift)
		}
		cursor = maxAddr(cursor, childEnd)
		if addrCmp(cursor, hi) >= 0 {
			return
		}
	}
	if addrCmp(cursor, hi) < 0 {
		s.emitGap(n, cursor, hi, shift, true)
	}
}

func minAddr(a, b Addr) Addr {
	if addrCmp(a, b) <= 0 {
		return a
	}
	return b
}

func maxAddr(a, b Addr) Addr {
	if addrCmp(a, b) >= 0 {
		return a
	}
	return b
}

func (s *resolveState) merge() {
	sort.SliceStable(s.pieces, func(i, j int) bool {
		c := addrCmp(s.pieces[i].LogLo, s.pieces[j].LogLo)
		if c != 0 {
			return c < 0
		}
		return addrCmp(s.pieces[i].LogHi, s.pieces[j].LogHi) < 0
	})
	out := s.pieces[:0]
	for _, p := range s.pieces {
		if len(out) > 0 {
			prev := &out[len(out)-1]
			if prev.Key == p.Key &&
				addrEqual(prev.LogHi, p.LogLo) &&
				addrEqual(prev.BlockHi, Addr64(p.BlockLo)) &&
				addrEqual(prev.CoordHi, Addr64(p.CoordLo)) {
				prev.LogHi = p.LogHi
				prev.BlockHi = p.BlockHi
				prev.CoordHi = p.CoordHi
				continue
			}
		}
		out = append(out, p)
	}
	s.pieces = out
	sort.SliceStable(s.gaps, func(i, j int) bool {
		return addrCmp(s.gaps[i].LogLo, s.gaps[j].LogLo) < 0
	})
}
