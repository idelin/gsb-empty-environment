package adaptivehuffman

import "encoding/binary"

// Exported-state layout (all integers big-endian, see FORMAT.md):
//
//	magic      uint32  "AHST" (0x41485354)
//	version    uint32  currently 1
//	maxWeight  uint64  rescale threshold
//	nodeCount  uint32  number of nodes N (ids are dense: 0..N-1)
//	nodes      N records, in increasing id order:
//	             symbol int32 (-2 internal, -1 NYT, 0..255 leaf)
//	             weight uint64
//	             parent int32 (node id, -1 for the root)
//	             left   int32 (node id, -1 for leaves)
//	             right  int32 (node id, -1 for leaves)
//	order      N x uint32: node ids sorted by rank (rank 0 first)
const (
	stateMagic      uint32 = 0x41485354
	stateVersion    uint32 = 1
	stateHeaderSize        = 4 + 4 + 8 + 4
	stateNodeSize          = 4 + 8 + 4 + 4 + 4
)

func (m *model) marshal() []byte {
	n := len(m.order)
	out := make([]byte, 0, stateHeaderSize+n*stateNodeSize+n*4)
	out = binary.BigEndian.AppendUint32(out, stateMagic)
	out = binary.BigEndian.AppendUint32(out, stateVersion)
	out = binary.BigEndian.AppendUint64(out, m.maxWeight)
	out = binary.BigEndian.AppendUint32(out, uint32(n))

	byID := make([]*node, n)
	for _, nd := range m.order {
		byID[nd.id] = nd
	}
	idOf := func(p *node) int32 {
		if p == nil {
			return -1
		}
		return int32(p.id)
	}
	for _, nd := range byID {
		out = binary.BigEndian.AppendUint32(out, uint32(nd.symbol))
		out = binary.BigEndian.AppendUint64(out, nd.weight)
		out = binary.BigEndian.AppendUint32(out, uint32(idOf(nd.parent)))
		out = binary.BigEndian.AppendUint32(out, uint32(idOf(nd.left)))
		out = binary.BigEndian.AppendUint32(out, uint32(idOf(nd.right)))
	}
	for _, nd := range m.order {
		out = binary.BigEndian.AppendUint32(out, uint32(nd.id))
	}
	return out
}

func unmarshalModel(data []byte) (*model, error) {
	corrupt := func(off int64) error {
		return &Error{Kind: ErrKindCorruptState, Byte: off, Bit: 0}
	}
	if len(data) < stateHeaderSize {
		return nil, corrupt(int64(len(data)))
	}
	if binary.BigEndian.Uint32(data[0:4]) != stateMagic {
		return nil, corrupt(0)
	}
	if binary.BigEndian.Uint32(data[4:8]) != stateVersion {
		return nil, corrupt(4)
	}
	maxWeight := binary.BigEndian.Uint64(data[8:16])
	if maxWeight < 2 {
		return nil, corrupt(8)
	}
	n := int(binary.BigEndian.Uint32(data[16:20]))
	if n < 1 || n > 2*numSymbols+1 {
		return nil, corrupt(16)
	}
	if want := stateHeaderSize + n*stateNodeSize + n*4; len(data) != want {
		return nil, corrupt(int64(len(data)))
	}

	type rawNode struct {
		symbol              int32
		weight              uint64
		parent, left, right int32
	}
	raws := make([]rawNode, n)
	off := stateHeaderSize
	inRange := func(v int32) bool { return v == -1 || (v >= 0 && v < int32(n)) }
	for i := 0; i < n; i++ {
		sym := int32(binary.BigEndian.Uint32(data[off:]))
		w := binary.BigEndian.Uint64(data[off+4:])
		p := int32(binary.BigEndian.Uint32(data[off+12:]))
		l := int32(binary.BigEndian.Uint32(data[off+16:]))
		r := int32(binary.BigEndian.Uint32(data[off+20:]))
		if sym != symNYT && sym != symInternal && (sym < 0 || sym >= numSymbols) {
			return nil, corrupt(int64(off))
		}
		if !inRange(p) || !inRange(l) || !inRange(r) {
			return nil, corrupt(int64(off + 12))
		}
		if sym == symInternal {
			if l < 0 || r < 0 {
				return nil, corrupt(int64(off + 16))
			}
		} else if l != -1 || r != -1 {
			return nil, corrupt(int64(off + 16))
		}
		raws[i] = rawNode{symbol: sym, weight: w, parent: p, left: l, right: r}
		off += stateNodeSize
	}

	orderIDs := make([]int, n)
	seen := make([]bool, n)
	for i := 0; i < n; i++ {
		id := int(binary.BigEndian.Uint32(data[off:]))
		if id < 0 || id >= n || seen[id] {
			return nil, corrupt(int64(off))
		}
		seen[id] = true
		orderIDs[i] = id
		off += 4
	}

	nodes := make([]*node, n)
	for i := 0; i < n; i++ {
		nodes[i] = &node{id: i, symbol: raws[i].symbol, weight: raws[i].weight}
	}
	link := func(idx int32) *node {
		if idx < 0 {
			return nil
		}
		return nodes[idx]
	}
	m := &model{maxWeight: maxWeight, nextID: n}
	var root, nyt *node
	for i := 0; i < n; i++ {
		nd := nodes[i]
		nd.parent = link(raws[i].parent)
		nd.left = link(raws[i].left)
		nd.right = link(raws[i].right)
		if nd.parent == nil {
			if root != nil {
				return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize + 12)
			}
			root = nd
		}
		switch nd.symbol {
		case symNYT:
			if nyt != nil {
				return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize)
			}
			nyt = nd
		case symInternal:
			// nothing to record
		default:
			if m.leaves[nd.symbol] != nil {
				return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize)
			}
			m.leaves[nd.symbol] = nd
			m.numSyms++
		}
	}
	if root == nil || nyt == nil {
		return nil, corrupt(0)
	}
	for i := 0; i < n; i++ {
		nd := nodes[i]
		if p := nd.parent; p != nil && p.left != nd && p.right != nd {
			return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize + 12)
		}
		if nd.left != nil && nd.left.parent != nd {
			return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize + 16)
		}
		if nd.right != nil && nd.right.parent != nd {
			return nil, corrupt(stateHeaderSize + int64(i)*stateNodeSize + 20)
		}
	}

	m.order = make([]*node, n)
	for rank, id := range orderIDs {
		nd := nodes[id]
		nd.rank = rank
		m.order[rank] = nd
	}
	m.root = root
	m.nyt = nyt
	return m, nil
}
