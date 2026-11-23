package envguard

import (
	"fmt"
	"math/bits"
	"unicode"
)

// RuneLen returns the rune length without expanding the rope.
func (g *Graph) RuneLen(id NodeID) int64 { return g.nodes[id].len }

// RuneAt returns the rune at rune offset i.
func (g *Graph) RuneAt(id NodeID, i int64) rune {
	r, err := g.runeAt(id, i)
	if err != nil {
		panic(err)
	}
	return r
}

func (g *Graph) runeAt(id NodeID, i int64) (rune, error) {
	if i < 0 || i >= g.nodes[id].len {
		return 0, fmt.Errorf("envguard: offset %d out of range", i)
	}
	n := g.nodes[id]
	switch n.kind {
	case nLiteral:
		return []rune(n.text)[i], nil
	case nVar:
		return []rune(g.reg.valueOf(n.origin))[i], nil
	case nConcat:
		for _, p := range n.parts {
			pl := g.nodes[p].len
			if i < pl {
				return g.runeAt(p, i)
			}
			i -= pl
		}
	case nLoop:
		bl := g.nodes[n.body].len
		return g.runeAt(n.body, i%bl)
	case nCond:
		return g.runeAt(g.chosenBranch(n), i)
	case nFold:
		r, err := g.runeAt(n.child, i)
		if err != nil {
			return 0, err
		}
		return foldRune(r), nil
	case nSlice:
		return g.runeAt(n.child, n.lo+i)
	case nSanitize:
		if n.san.Fixed != nil {
			return n.san.mapFn(g.nodeSeq(n.child), i), nil
		}
		in, err := g.mapRemove(n, i)
		if err != nil {
			return 0, err
		}
		return g.runeAt(n.child, in)
	}
	return 0, fmt.Errorf("envguard: unsupported node kind")
}

func (g *Graph) chosenBranch(n *node) NodeID {
	o := n.selector
	rec := g.reg.inputs[originKey{o.Tenant, o.Name}]
	if rec.dfa.Accepts(runeSeqOf(rec.value)) {
		return n.child
	}
	return n.childB
}

func foldRune(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return unicode.ToLower(r)
}

// nodeSeq adapts a node to RuneSeq so sanitizer mapping functions can read
// the input rope without materializing it.
func (g *Graph) nodeSeq(id NodeID) RuneSeq { return &nodeSeq{g: g, id: id} }

type nodeSeq struct {
	g  *Graph
	id NodeID
}

func (s *nodeSeq) RuneLen() int64      { return s.g.RuneLen(s.id) }
func (s *nodeSeq) RuneAt(i int64) rune { return s.g.RuneAt(s.id, i) }

// CharInfo explains a single output rune.
type CharInfo struct {
	Offset  int64
	Rune    rune
	Content []*Origin
	Control []*Origin
}

// ProvenanceAt returns the content and control origins of the rune at i.
// Content origins name where the character (or trusted rewrite of it) comes
// from; control origins name every binding that could decide whether this
// rune exists at this position.
func (g *Graph) ProvenanceAt(id NodeID, i int64) (CharInfo, error) {
	if i < 0 || i >= g.nodes[id].len {
		return CharInfo{}, fmt.Errorf("envguard: offset %d out of range", i)
	}
	data := newBset(len(g.reg.origins))
	control := newBset(len(g.reg.origins))
	g.collect(id, i, data, control)
	return CharInfo{
		Offset:  i,
		Rune:    g.RuneAt(id, i),
		Content: g.originsOf(data),
		Control: g.originsOf(control),
	}, nil
}

func (g *Graph) originsOf(s bset) []*Origin {
	var out []*Origin
	for i, w := range s.w {
		for w != 0 {
			bit := i*64 + trailing(w)
			out = append(out, g.reg.origins[bit])
			w &= w - 1
		}
	}
	return out
}

func trailing(w uint64) int {
	return bits.TrailingZeros64(w)
}

func (g *Graph) collect(id NodeID, i int64, data, control bset) {
	n := g.nodes[id]
	switch n.kind {
	case nLiteral:
		data.w[n.origin>>6] |= 1 << uint(n.origin&63)
	case nVar:
		bit := n.origin
		data.w[bit>>6] |= 1 << uint(bit&63)
		control.w[bit>>6] |= 1 << uint(bit&63)
	case nConcat:
		for _, p := range n.parts {
			pl := g.nodes[p].len
			if i < pl {
				g.collect(p, i, data, control)
				return
			}
			i -= pl
		}
	case nLoop:
		g.collect(n.body, i%g.nodes[n.body].len, data, control)
	case nCond:
		g.collect(g.chosenBranch(n), i, data, control)
		la := g.nodes[n.child].len
		lb := g.nodes[n.childB].len
		common := la
		if lb < common {
			common = lb
		}
		if i >= common || g.RuneAt(n.child, i) != g.RuneAt(n.childB, i) {
			sel := n.selector.ID
			control.w[sel>>6] |= 1 << uint(sel&63)
		}
	case nFold:
		g.collect(n.child, i, data, control)
	case nSlice:
		g.collect(n.child, n.lo+i, data, control)
	case nSanitize:
		if n.san.Fixed != nil {
			// Trusted rewrite: content carries no tenant origin; branch
			// control still flows through.
			g.orInto(control, n.branchCt)
			return
		}
		in, err := g.mapRemove(n, i)
		if err != nil {
			panic(err)
		}
		g.collect(n.child, in, data, control)
	}
}

func (g *Graph) orInto(dst, src bset) {
	for i, w := range src.w {
		dst.w[i] |= w
	}
}
