package archive

import (
	"bytes"
	"testing"
)

func kitchenBlocks() []Block {
	return []Block{
		{ID: 1, Size: 8, Codec: "raw"},
		{ID: 2, Size: 6, Codec: "raw"},
	}
}

func kitchenData() map[BlockID][]byte {
	return map[BlockID][]byte{
		1: []byte("ABCDEFGH"),
		2: []byte("ijklmn"),
	}
}

// kitchenNodes returns a small graph that exercises raw, dict, slice, reverse,
// repeat and concat nodes, including the same physical block at several root
// positions.
func kitchenNodes() []Node {
	return []Node{
		NewRawNode(0, 1),
		NewRawNode(1, 2),
		NewDictNode(2, 1, 2, 4),    // CDEF
		NewReverseNode(3, 2),       // FEDC
		NewSliceNode(4, 0, 2, 4),   // CDEF
		NewRepeatNode(5, 4, 3),     // CDEF x3
		NewDictNode(6, 2, 1, 3),    // jkl
		NewConcatNode(7, 0, 2, 6),  // ABCDEFGH + CDEF + jkl
		NewReverseNode(8, 7),       // reverse of node 7
		NewRepeatNode(9, 5, 2),     // CDEF x6
		NewConcatNode(10, 2, 3),    // CDEF + FEDC (same block twice)
		NewSliceNode(11, 7, 3, 9),  // DEF + GH + CDEF? slice of node 7
		NewReverseNode(12, 9),      // FEDC x6
		NewDictNode(13, 1, 5, 3),   // FGH
		NewConcatNode(14, 13, 2),   // FGH + CDEF
		NewRepeatNode(15, 14, 2),   // FGHCDEFFGHCDEF
		NewConcatNode(16, 1, 0),    // ijklmn + ABCDEFGH
		NewRepeatNode(17, 1, 4),    // ijklmn x4
		NewSliceNode(18, 8, 2, 10), // slice of a reverse
		NewReverseSliceNode(19, 0, 2, 4),
	}
}

func strategyDigest(s string) Digest { return sha256Digest([]byte(s)) }

func kitchenManifest(t *testing.T, root NodeID, version uint64) *Manifest {
	t.Helper()
	m := &Manifest{
		Version:        version,
		Strategy:       "raw-chain",
		StrategyDigest: strategyDigest("raw-chain"),
		Root:           root,
		Nodes:          kitchenNodes(),
		Blocks:         kitchenBlocks(),
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal kitchen manifest: %v", err)
	}
	return m
}

// refExpand expands a node with an intentionally simple, independent
// implementation.  It is used only for small graphs.
func refExpand(t *testing.T, m *Manifest, root NodeID, limit int) []byte {
	t.Helper()
	nm, err := buildNodeMap(m)
	if err != nil {
		t.Fatalf("node map: %v", err)
	}
	pages := kitchenData()
	memo := make(map[NodeID][]byte)
	var build func(NodeID) []byte
	build = func(id NodeID) []byte {
		if b, ok := memo[id]; ok {
			return b
		}
		n := m.Nodes[nm.byID[id]]
		var out []byte
		switch n.Kind {
		case KindRaw:
			out = append([]byte(nil), pages[n.Block]...)
		case KindDict:
			out = append([]byte(nil), pages[n.Block][n.Off:n.Off+n.Length]...)
		case KindSlice:
			child := build(n.Child)
			out = append([]byte(nil), child[n.Off:n.Off+n.Length]...)
		case KindReverse:
			child := build(n.Child)
			out = make([]byte, len(child))
			for i := range child {
				out[len(child)-1-i] = child[i]
			}
		case KindRepeat:
			child := build(n.Child)
			for i := uint64(0); i < n.Count; i++ {
				out = append(out, child...)
				if len(out) > limit {
					t.Fatalf("ref expand exceeded limit")
				}
			}
		case KindConcat:
			for _, cid := range n.Children {
				out = append(out, build(cid)...)
			}
		case KindReverseSlice:
			child := build(n.Child)
			chunk := child[n.Off : n.Off+n.Length]
			out = make([]byte, len(chunk))
			for i := range chunk {
				out[len(chunk)-1-i] = chunk[i]
			}
		default:
			t.Fatalf("bad kind %d", n.Kind)
		}
		memo[id] = out
		return out
	}
	return build(root)
}

func mustResolve(t *testing.T, m *Manifest, start, end uint64) []*Piece {
	t.Helper()
	ps, err := Resolve(m, start, end)
	if err != nil {
		t.Fatalf("resolve [%d,%d): %v", start, end, err)
	}
	return ps
}

func materializeResolved(t *testing.T, ps []*Piece, pages map[BlockID][]byte) []byte {
	t.Helper()
	out, err := Materialize(ps, pages)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return out
}

func countPieces(ps []*Piece) int {
	n := 0
	var walk func([]*Piece)
	walk = func(list []*Piece) {
		for _, p := range list {
			n++
			if p != nil && !p.IsLeaf() {
				walk(p.Parts)
			}
		}
	}
	walk(ps)
	return n
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }

// sharedBlockManifest is B1 | B2 | B1 | B3, so root requests at [0,4) and
// [8,12) share one physical page at different root positions.
func sharedBlockManifest(t *testing.T) (*Manifest, map[BlockID][]byte) {
	t.Helper()
	blocks := []Block{
		{ID: 1, Size: 4, Codec: "raw"},
		{ID: 2, Size: 4, Codec: "raw"},
		{ID: 3, Size: 4, Codec: "raw"},
	}
	pages := map[BlockID][]byte{
		1: []byte("aaaa"),
		2: []byte("bbbb"),
		3: []byte("cccc"),
	}
	nodes := []Node{
		NewRawNode(0, 1),
		NewRawNode(1, 2),
		NewRawNode(2, 3),
		NewConcatNode(3, 0, 1, 0, 2),
	}
	m := &Manifest{
		Version:        1,
		Strategy:       "raw",
		StrategyDigest: strategyDigest("raw"),
		Root:           3,
		Nodes:          nodes,
		Blocks:         blocks,
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal shared manifest: %v", err)
	}
	return m, pages
}
