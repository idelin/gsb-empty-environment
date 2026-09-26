package pathtree

import (
	"encoding/binary"
	"hash/fnv"
	"reflect"
	"unsafe"
)

type node struct {
	key    string
	value  []byte
	left   *node
	right  *node
	size   uint64
	height uint8
}

// Tree is an immutable persistent AVL tree. Every update returns a new Tree
// and leaves the receiver unchanged.
type Tree struct {
	root *node
}

// BatchStats reports allocations made by a batch operation.
type BatchStats struct {
	CopiedNodes    int
	BuiltNodes     int
	Superseded     int
	NewNodes       int
	AbsentDeletes  int
	PresentDeletes int
}

type batchStats struct {
	copied        int
	built         int
	superseded    int
	absentDelete  int
	presentDelete int
}

func (s *batchStats) public() BatchStats {
	if s == nil {
		return BatchStats{}
	}
	return BatchStats{
		CopiedNodes:    s.copied,
		BuiltNodes:     s.built,
		Superseded:     s.superseded,
		NewNodes:       s.copied + s.built,
		AbsentDeletes:  s.absentDelete,
		PresentDeletes: s.presentDelete,
	}
}

// New returns an empty tree.
func New() *Tree {
	return &Tree{}
}

// Len returns the number of key/value pairs in the tree.
func (t *Tree) Len() int {
	if t == nil || t.root == nil {
		return 0
	}
	return int(t.root.size)
}

// Get returns a copy of the value associated with key. The second result
// reports whether the key exists.
func (t *Tree) Get(key string) ([]byte, bool) {
	n := t.root.find(key)
	if n == nil {
		return nil, false
	}
	return cloneBytes(n.value), true
}

// Has reports whether key exists in the tree.
func (t *Tree) Has(key string) bool {
	return t.root.find(key) != nil
}

// Rank returns the zero-based rank of key: the number of keys less than key.
// The second result reports whether key exists in the tree.
func (t *Tree) Rank(key string) (uint64, bool) {
	var rank uint64
	for n := t.root; n != nil; {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			rank += nodeSize(n.left) + 1
			n = n.right
		default:
			return rank + nodeSize(n.left), true
		}
	}
	return rank, false
}

// Select returns the key at zero-based rank r.
func (t *Tree) Select(r uint64) (string, []byte, error) {
	if r >= uint64(t.Len()) {
		return "", nil, &RangeError{Rank: r, Len: uint64(t.Len())}
	}
	n := t.root.selectNode(r)
	return n.key, cloneBytes(n.value), nil
}

// Digest returns an FNV-1a digest of the complete tree contents and order.
func (t *Tree) Digest() [8]byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], rootSum(t.root))
	return out
}

// DigestString returns the digest as a lowercase hexadecimal string.
func (t *Tree) DigestString() string {
	d := t.Digest()
	const digits = "0123456789abcdef"
	b := make([]byte, 16)
	for i, v := range d {
		b[i*2] = digits[v>>4]
		b[i*2+1] = digits[v&15]
	}
	return string(b)
}

// FieldLayout describes the real offset and size of one struct field.
type FieldLayout struct {
	Name   string
	Offset uintptr
	Size   uintptr
}

// Layout reports the compiler-selected in-memory layout of internal nodes.
type Layout struct {
	TypeName string
	Size     uintptr
	Fields   []FieldLayout
}

// NodeSize returns unsafe.Sizeof(node{}), the header bytes used by one node.
func NodeSize() uintptr {
	return unsafe.Sizeof(node{})
}

// TreeSize returns unsafe.Sizeof(Tree{}).
func TreeSize() uintptr {
	return unsafe.Sizeof(Tree{})
}

// NodeLayout returns the actual field offsets and sizes for node.
func NodeLayout() Layout {
	typ := reflect.TypeOf(node{})
	layout := Layout{
		TypeName: typ.String(),
		Size:     typ.Size(),
		Fields:   make([]FieldLayout, 0, typ.NumField()),
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		layout.Fields = append(layout.Fields, FieldLayout{
			Name:   field.Name,
			Offset: field.Offset,
			Size:   field.Type.Size(),
		})
	}
	return layout
}

// Footprint is the logical reachable byte usage of one immutable tree version.
// It does not include allocator size classes, garbage collector metadata, or
// capacity beyond the live length of copied strings and byte slices.
type Footprint struct {
	UniqueNodes int
	NodeBytes   uint64
	KeyBytes    uint64
	ValueBytes  uint64
	TotalBytes  uint64
}

// LogicalFootprint walks one version and counts each reachable node once.
func (t *Tree) LogicalFootprint() Footprint {
	if t == nil {
		return Footprint{}
	}
	nodeHeader := uint64(NodeSize())
	seen := make(map[*node]struct{})
	var f Footprint

	var walk func(*node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		f.UniqueNodes++
		f.NodeBytes += nodeHeader
		f.KeyBytes += uint64(len(n.key))
		f.ValueBytes += uint64(len(n.value))
		walk(n.left)
		walk(n.right)
	}
	walk(t.root)
	f.TotalBytes = f.NodeBytes + f.KeyBytes + f.ValueBytes
	return f
}

// RangeError indicates that Select received an out-of-range rank.
type RangeError struct {
	Rank uint64
	Len  uint64
}

func (e *RangeError) Error() string {
	return "pathtree: rank " + uitoa(e.Rank) + " out of range for tree of length " + uitoa(e.Len)
}

func (n *node) find(key string) *node {
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

func (n *node) selectNode(r uint64) *node {
	for {
		leftSize := nodeSize(n.left)
		switch {
		case r < leftSize:
			n = n.left
		case r == leftSize:
			return n
		default:
			r -= leftSize + 1
			n = n.right
		}
	}
}

func nodeSize(n *node) uint64 {
	if n == nil {
		return 0
	}
	return n.size
}

func nodeHeight(n *node) uint8 {
	if n == nil {
		return 0
	}
	return n.height
}

func balanceFactor(n *node) int {
	return int(nodeHeight(n.left)) - int(nodeHeight(n.right))
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func writeUint64(h interface {
	Write([]byte) (int, error)
}, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = h.Write(b[:])
}

func nodeSum(n *node) uint64 {
	if n == nil {
		return fnv.New64a().Sum64()
	}
	h := fnv.New64a()
	writeUint64(h, nodeSum(n.left))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(n.key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(n.value)
	_, _ = h.Write([]byte{0})
	writeUint64(h, nodeSum(n.right))
	writeUint64(h, n.size)
	writeUint64(h, uint64(n.height))
	return h.Sum64()
}

func rootSum(n *node) uint64 {
	return nodeSum(n)
}
