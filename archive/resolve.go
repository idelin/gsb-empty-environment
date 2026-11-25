package archive

import (
	"fmt"
	"sort"
)

// Piece is a compact description of a contiguous portion of the expanded root
// coordinate space.
//
// A leaf piece (Parts == nil) copies a contiguous physical block range.
// A repeat piece describes a pattern (Parts) repeated Count times; this is what
// keeps a 2^48-byte repeated subtree compact instead of expanding it per byte.
//
// Invariants:
//
//	leaf:   Len == NLen, Pattern == NLen, Count == 1
//	repeat: Pattern == sum(part.Len), Len == Pattern*Count, Count > 1
type Piece struct {
	Len     uint64
	Pattern uint64
	Count   uint64
	Parts   []*Piece

	// Leaf fields.
	Block BlockID
	Off   uint64
	NLen  uint64
	Rev   bool
}

// IsLeaf reports whether the piece directly references a physical block.
func (p *Piece) IsLeaf() bool { return p.Parts == nil }

func leafPiece(block BlockID, off, nlen uint64, rev bool) *Piece {
	return &Piece{Len: nlen, Pattern: nlen, Count: 1, Block: block, Off: off, NLen: nlen, Rev: rev}
}

func repeatPiece(parts []*Piece, count uint64) (*Piece, error) {
	if count == 0 {
		return nil, fmt.Errorf("%w: repeat piece with zero count", ErrInvalidManifest)
	}
	var pattern uint64
	for _, p := range parts {
		if p == nil {
			return nil, fmt.Errorf("%w: nil repeat part", ErrInvalidManifest)
		}
		if p.Len == 0 {
			return nil, fmt.Errorf("%w: empty repeat part", ErrInvalidManifest)
		}
		if p.Len > MaxExpandedLength-pattern {
			return nil, fmt.Errorf("%w: repeat pattern exceeds 2^48", ErrInvalidManifest)
		}
		pattern += p.Len
	}
	if len(parts) == 0 || pattern == 0 {
		return nil, fmt.Errorf("%w: empty repeat pattern", ErrInvalidManifest)
	}
	if count > MaxExpandedLength/pattern {
		return nil, fmt.Errorf("%w: repeat expands beyond 2^48", ErrInvalidManifest)
	}
	if count == 1 && len(parts) == 1 {
		return parts[0], nil
	}
	return &Piece{Len: pattern * count, Pattern: pattern, Count: count, Parts: parts}, nil
}

// Resolve maps the root-coordinate interval [start,end) to a compact list of
// pieces.  The result never enumerates repeated subtree copies: a range that
// spans many copies is represented by a repeat piece.
//
// The pieces are in output order and exactly cover [start,end).  A piece's
// physical range is never widened to a cache-page or block boundary.
func Resolve(m *Manifest, start, end uint64) ([]*Piece, error) {
	if err := m.PrepareLengths(); err != nil {
		return nil, err
	}
	if end < start {
		return nil, fmt.Errorf("%w: reversed interval [%d,%d)", ErrInvalidManifest, start, end)
	}
	if end > m.TotalLength {
		return nil, fmt.Errorf("%w: interval [%d,%d) beyond total length %d", ErrInvalidManifest, start, end, m.TotalLength)
	}
	if start == end {
		return nil, nil
	}
	nm, err := buildNodeMap(m)
	if err != nil {
		return nil, err
	}
	r := &resolver{m: m, nm: nm}
	parts, err := r.resolveNode(m.Root, start, end)
	if err != nil {
		return nil, err
	}
	if err := checkPieces(parts, end-start, ""); err != nil {
		return nil, err
	}
	return parts, nil
}

type resolver struct {
	m  *Manifest
	nm *nodeMap
}

func (r *resolver) span(id NodeID) uint64 {
	idx, ok := r.nm.byID[id]
	if !ok {
		return 0
	}
	return r.m.Nodes[idx].Span
}

func (r *resolver) resolveNode(id NodeID, a, b uint64) ([]*Piece, error) {
	idx, ok := r.nm.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: node %d missing", ErrInvalidManifest, id)
	}
	n := r.m.Nodes[idx]
	if b < a || b > n.Span || a > n.Span {
		return nil, fmt.Errorf("%w: node %d interval [%d,%d) outside span %d", ErrInvalidManifest, id, a, b, n.Span)
	}
	if a == b {
		return nil, nil
	}
	switch n.Kind {
	case KindRaw:
		bdef, ok := r.nm.blocks[n.Block]
		if !ok {
			return nil, fmt.Errorf("%w: node %d missing block %d", ErrInvalidManifest, id, n.Block)
		}
		if b > bdef.Size {
			return nil, fmt.Errorf("%w: node %d reads past block %d", ErrInvalidManifest, id, n.Block)
		}
		return []*Piece{leafPiece(n.Block, a, b-a, false)}, nil

	case KindDict:
		bdef, ok := r.nm.blocks[n.Block]
		if !ok {
			return nil, fmt.Errorf("%w: node %d missing block %d", ErrInvalidManifest, id, n.Block)
		}
		off := n.Off + a
		if off < n.Off || off > bdef.Size || b-a > bdef.Size-off {
			return nil, fmt.Errorf("%w: node %d dict slice leaves block %d", ErrInvalidManifest, id, n.Block)
		}
		return []*Piece{leafPiece(n.Block, off, b-a, false)}, nil

	case KindSlice:
		return r.resolveNode(n.Child, n.Off+a, n.Off+b)

	case KindReverse:
		childSpan := r.span(n.Child)
		ps, err := r.resolveNode(n.Child, childSpan-b, childSpan-a)
		if err != nil {
			return nil, err
		}
		return reversePieces(ps), nil

	case KindRepeat:
		childSpan := r.span(n.Child)
		if childSpan == 0 {
			return nil, fmt.Errorf("%w: node %d repeats empty child", ErrInvalidManifest, id)
		}
		var out []*Piece
		pos := a

		if pos%childSpan != 0 {
			copyEnd := (pos/childSpan + 1) * childSpan
			if copyEnd > b {
				copyEnd = b
			}
			ps, err := r.resolveNode(n.Child, pos%childSpan, pos%childSpan+(copyEnd-pos))
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
			pos = copyEnd
		}

		fullEnd := b - b%childSpan
		if pos < fullEnd {
			count := (fullEnd - pos) / childSpan
			pattern, err := r.resolveNode(n.Child, 0, childSpan)
			if err != nil {
				return nil, err
			}
			if count == 1 {
				out = append(out, pattern...)
			} else {
				rp, err := repeatPiece(pattern, count)
				if err != nil {
					return nil, err
				}
				out = append(out, rp)
			}
			pos = fullEnd
		}

		if pos < b {
			ps, err := r.resolveNode(n.Child, 0, b-pos)
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%w: node %d resolved to nothing", ErrInvalidManifest, id)
		}
		return out, nil

	case KindConcat:
		var out []*Piece
		base := uint64(0)
		for _, cid := range n.Children {
			cs := r.span(cid)
			lo := base
			hi := base + cs
			if b <= lo {
				break
			}
			if a >= hi {
				base = hi
				continue
			}
			start, end := a, b
			if start < lo {
				start = lo
			}
			if end > hi {
				end = hi
			}
			ps, err := r.resolveNode(cid, start-lo, end-lo)
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
			base = hi
		}
		if len(out) == 0 && b > a {
			return nil, fmt.Errorf("%w: concat node %d resolved to nothing", ErrInvalidManifest, id)
		}
		return out, nil

	case KindReverseSlice:
		// Output x in [a,b) is child[Off+Length-1-(x-a)].
		hi := n.Off + n.Length - a
		lo := n.Off + n.Length - b
		ps, err := r.resolveNode(n.Child, lo, hi)
		if err != nil {
			return nil, err
		}
		return reversePieces(ps), nil
	}
	return nil, fmt.Errorf("%w: node %d has unknown kind %d", ErrInvalidManifest, id, n.Kind)
}

func reversePieces(ps []*Piece) []*Piece {
	if len(ps) == 0 {
		return nil
	}
	out := make([]*Piece, len(ps))
	for i, p := range ps {
		out[len(ps)-1-i] = reversePiece(p)
	}
	return out
}

func reversePiece(p *Piece) *Piece {
	if p == nil {
		return nil
	}
	if p.IsLeaf() {
		cp := *p
		cp.Rev = !cp.Rev
		return &cp
	}
	return &Piece{
		Len:     p.Len,
		Pattern: p.Pattern,
		Count:   p.Count,
		Parts:   reversePieces(p.Parts),
	}
}

func checkPieces(parts []*Piece, want uint64, path string) error {
	var total uint64
	for _, p := range parts {
		if p == nil {
			return fmt.Errorf("%w: nil piece", ErrInvalidManifest)
		}
		if p.Len == 0 {
			return fmt.Errorf("%w: zero-length piece", ErrInvalidManifest)
		}
		if p.IsLeaf() {
			if p.NLen == 0 || p.NLen != p.Len || p.Count != 1 || p.Pattern != p.NLen {
				return fmt.Errorf("%w: malformed leaf piece", ErrInvalidManifest)
			}
		} else {
			if p.Count < 1 || p.Pattern == 0 {
				return fmt.Errorf("%w: malformed repeat piece", ErrInvalidManifest)
			}
			var pat uint64
			for _, c := range p.Parts {
				if c == nil || c.Len == 0 {
					return fmt.Errorf("%w: malformed repeat child", ErrInvalidManifest)
				}
				if c.Len > MaxExpandedLength-pat {
					return fmt.Errorf("%w: repeat pattern overflow", ErrInvalidManifest)
				}
				pat += c.Len
			}
			if pat != p.Pattern {
				return fmt.Errorf("%w: repeat pattern mismatch", ErrInvalidManifest)
			}
			if p.Pattern > MaxExpandedLength/p.Count {
				return fmt.Errorf("%w: piece length overflow", ErrInvalidManifest)
			}
			if p.Len != p.Pattern*p.Count {
				return fmt.Errorf("%w: repeat length mismatch", ErrInvalidManifest)
			}
		}
		if total > MaxExpandedLength-p.Len {
			return fmt.Errorf("%w: piece total overflow", ErrInvalidManifest)
		}
		total += p.Len
	}
	if total != want {
		return fmt.Errorf("%w: pieces cover %d bytes, want %d", ErrInvalidManifest, total, want)
	}
	return nil
}

// SourceBlocks returns the distinct physical blocks referenced by the pieces.
// It walks each repeat pattern once and never expands the repetition count.
func SourceBlocks(parts []*Piece) []BlockID {
	set := make(map[BlockID]struct{})
	var walk func([]*Piece)
	walk = func(ps []*Piece) {
		for _, p := range ps {
			if p == nil {
				continue
			}
			if p.IsLeaf() {
				set[p.Block] = struct{}{}
				continue
			}
			walk(p.Parts)
		}
	}
	walk(parts)
	out := make([]BlockID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// LeafVisit describes one concrete leaf occurrence when walking a bounded
// amount of expanded output.
type LeafVisit struct {
	RootOffset uint64
	Block      BlockID
	Off        uint64
	NLen       uint64
	Rev        bool
}

// WalkLeaves visits every concrete leaf occurrence.  It is intended for tests,
// diagnostics and small outputs; callers must bound the expanded length before
// calling it.
func WalkLeaves(parts []*Piece, fn func(LeafVisit)) {
	var walk func([]*Piece, uint64) uint64
	walk = func(ps []*Piece, root uint64) uint64 {
		for _, p := range ps {
			if p == nil {
				continue
			}
			if p.IsLeaf() {
				fn(LeafVisit{RootOffset: root, Block: p.Block, Off: p.Off, NLen: p.NLen, Rev: p.Rev})
				root += p.NLen
				continue
			}
			for c := uint64(0); c < p.Count; c++ {
				root = walk(p.Parts, root)
			}
		}
		return root
	}
	walk(parts, 0)
}

// Materialize copies bytes for the pieces from a map of decoded physical pages.
// Only the exact offsets recorded in the pieces are read; a page may contain
// adjacent unauthorized bytes, but they can never be returned through this
// function.
func Materialize(parts []*Piece, pages map[BlockID][]byte) ([]byte, error) {
	var out []byte
	var appendPiece func(p *Piece) error
	appendPiece = func(p *Piece) error {
		if p == nil {
			return fmt.Errorf("%w: nil piece", ErrInvalidManifest)
		}
		if p.IsLeaf() {
			page, ok := pages[p.Block]
			if !ok {
				return fmt.Errorf("%w: page for block %d is not resident", ErrInvalidManifest, p.Block)
			}
			if p.Off > uint64(len(page)) || p.NLen > uint64(len(page))-p.Off {
				return fmt.Errorf("%w: leaf block %d range [%d,%d) outside decoded page length %d",
					ErrInvalidManifest, p.Block, p.Off, p.Off+p.NLen, len(page))
			}
			start := len(out)
			out = append(out, page[p.Off:p.Off+p.NLen]...)
			if p.Rev {
				for i, j := start, len(out)-1; i < j; i, j = i+1, j-1 {
					out[i], out[j] = out[j], out[i]
				}
			}
			return nil
		}
		for c := uint64(0); c < p.Count; c++ {
			for _, child := range p.Parts {
				if err := appendPiece(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, p := range parts {
		if err := appendPiece(p); err != nil {
			return nil, err
		}
	}
	expected := uint64(0)
	for _, p := range parts {
		expected += p.Len
	}
	if uint64(len(out)) != expected {
		return nil, fmt.Errorf("%w: materialized %d bytes, want %d", ErrInvalidManifest, len(out), expected)
	}
	return out, nil
}

// EqualPieces reports whether two piece lists are structurally identical.
func EqualPieces(a, b []*Piece) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalPiece(a[i], b[i]) {
			return false
		}
	}
	return true
}

func equalPiece(a, b *Piece) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Len != b.Len || a.Pattern != b.Pattern || a.Count != b.Count {
		return false
	}
	if a.IsLeaf() != b.IsLeaf() {
		return false
	}
	if a.IsLeaf() {
		return a.Block == b.Block && a.Off == b.Off && a.NLen == b.NLen && a.Rev == b.Rev
	}
	if len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if !equalPiece(a.Parts[i], b.Parts[i]) {
			return false
		}
	}
	return true
}

// ClonePieces deep-copies a piece list.
func ClonePieces(ps []*Piece) []*Piece {
	if ps == nil {
		return nil
	}
	out := make([]*Piece, len(ps))
	for i, p := range ps {
		out[i] = clonePiece(p)
	}
	return out
}

func clonePiece(p *Piece) *Piece {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Parts != nil {
		cp.Parts = ClonePieces(p.Parts)
	}
	return &cp
}
