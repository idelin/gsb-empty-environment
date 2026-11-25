package archive

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// MaxExpandedLength is the largest supported expanded root length (2^48).
// Lengths are kept in uint64 throughout; the limit merely keeps every checked
// multiplication far away from uint64 overflow and documents the acceptance
// requirement.
const MaxExpandedLength uint64 = 1 << 48

// BlockID identifies a physical block.  Two references with the same BlockID
// share decoded bytes (and therefore decode-cache pages) but never share
// authorization: authorization is expressed in root coordinates.
type BlockID uint64

// NodeID identifies a node in a Manifest.
type NodeID uint64

// Digest is a SHA-256 digest.
type Digest [32]byte

func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Equal reports whether two digests are identical.
func (d Digest) Equal(o Digest) bool {
	return subtle.ConstantTimeCompare(d[:], o[:]) == 1
}

// IsZero reports whether the digest is the all-zero value.
func (d Digest) IsZero() bool {
	var zero Digest
	return d.Equal(zero)
}

func sha256Digest(parts ...[]byte) Digest {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

func u64Bytes(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

// Block describes one physical block.
type Block struct {
	ID    BlockID
	Size  uint64 // uncompressed size
	Codec string
}

// NodeKind enumerates the manifest node kinds.
type NodeKind uint8

const (
	// KindRaw emits the complete physical block named by Block.
	KindRaw NodeKind = iota + 1
	// KindDict emits Block[Off : Off+Length].
	KindDict
	// KindSlice emits Child[Off : Off+Length].
	KindSlice
	// KindReverse emits the reverse of Child.
	KindReverse
	// KindRepeat emits Child Count times.
	KindRepeat
	// KindConcat emits Children in order.
	KindConcat
	// KindReverseSlice emits Child[Off:Off+Length] in reverse order.
	KindReverseSlice
)

// Node is one manifest node.  ID must be unique in a manifest.  For KindRaw and
// KindDict, Block names the physical block.  For KindDict and KindSlice, Off and
// Length describe the slice.  For KindRepeat, Child and Count describe the
// repetition.  Span is the computed expanded length and is filled by
// Manifest.PrepareLengths / Seal.
type Node struct {
	ID       NodeID
	Kind     NodeKind
	Block    BlockID
	Off      uint64
	Length   uint64
	Child    NodeID
	Count    uint64
	Children []NodeID
	Span     uint64
}

// ExpandedLength returns the computed output length of the node.
func (n Node) ExpandedLength() uint64 { return n.Span }

// Constructors for the node kinds.  They set only the fields meaningful to the
// kind; Span is computed when the manifest is sealed.
func NewRawNode(id NodeID, block BlockID) Node {
	return Node{ID: id, Kind: KindRaw, Block: block}
}

func NewDictNode(id NodeID, block BlockID, off, length uint64) Node {
	return Node{ID: id, Kind: KindDict, Block: block, Off: off, Length: length}
}

func NewSliceNode(id, child NodeID, off, length uint64) Node {
	return Node{ID: id, Kind: KindSlice, Child: child, Off: off, Length: length}
}

func NewReverseNode(id, child NodeID) Node {
	return Node{ID: id, Kind: KindReverse, Child: child}
}

func NewRepeatNode(id, child NodeID, count uint64) Node {
	return Node{ID: id, Kind: KindRepeat, Child: child, Count: count}
}

func NewConcatNode(id NodeID, children ...NodeID) Node {
	return Node{ID: id, Kind: KindConcat, Children: children}
}

func NewReverseSliceNode(id, child NodeID, off, length uint64) Node {
	return Node{ID: id, Kind: KindReverseSlice, Child: child, Off: off, Length: length}
}

// Manifest is the signed archive description.
//
// The signature binds Version, TotalLength, Root, StrategyDigest and the
// content root digest of the manifest tree.  Thus changing a length, the root
// node, the compression-strategy descriptor, the manifest version, or any node
// or block record invalidates it.
type Manifest struct {
	Version        uint64
	Strategy       string
	StrategyDigest Digest
	Root           NodeID
	TotalLength    uint64
	Nodes          []Node
	Blocks         []Block

	// ContentRoot is computed by PrepareLengths/Seal.
	ContentRoot Digest
	Signature   Digest
}

var (
	// ErrInvalidManifest is returned for structurally invalid manifests.
	ErrInvalidManifest = errors.New("archive: invalid manifest")
	// ErrBadSignature is returned when the manifest signature does not verify.
	ErrBadSignature = errors.New("archive: bad manifest signature")
)

// NewManifest constructs, validates and seals a manifest.
func NewManifest(version uint64, strategy string, strategyDigest Digest, root NodeID, totalLength uint64, nodes []Node, blocks []Block) (*Manifest, error) {
	m := &Manifest{
		Version:        version,
		Strategy:       strategy,
		StrategyDigest: strategyDigest,
		Root:           root,
		TotalLength:    totalLength,
		Nodes:          append([]Node(nil), nodes...),
		Blocks:         append([]Block(nil), blocks...),
	}
	for i := range m.Nodes {
		m.Nodes[i].Children = append([]NodeID(nil), nodes[i].Children...)
	}
	if err := m.Seal(); err != nil {
		return nil, err
	}
	return m, nil
}

// Clone returns a deep copy of the manifest.
func (m *Manifest) Clone() *Manifest {
	if m == nil {
		return nil
	}
	out := *m
	out.Nodes = append([]Node(nil), m.Nodes...)
	for i := range out.Nodes {
		out.Nodes[i].Children = append([]NodeID(nil), m.Nodes[i].Children...)
	}
	out.Blocks = append([]Block(nil), m.Blocks...)
	return &out
}

func (m *Manifest) prepareNodeLengths() error {
	if m == nil {
		return ErrInvalidManifest
	}
	if len(m.Nodes) == 0 {
		return fmt.Errorf("%w: no nodes", ErrInvalidManifest)
	}

	blockIndex := make(map[BlockID]Block, len(m.Blocks))
	for _, b := range m.Blocks {
		if _, dup := blockIndex[b.ID]; dup {
			return fmt.Errorf("%w: duplicate block id %d", ErrInvalidManifest, b.ID)
		}
		if b.Size > MaxExpandedLength {
			return fmt.Errorf("%w: block %d exceeds 2^48", ErrInvalidManifest, b.ID)
		}
		blockIndex[b.ID] = b
	}

	nodeIndex := make(map[NodeID]int, len(m.Nodes))
	for i, n := range m.Nodes {
		if _, dup := nodeIndex[n.ID]; dup {
			return fmt.Errorf("%w: duplicate node id %d", ErrInvalidManifest, n.ID)
		}
		nodeIndex[n.ID] = i
	}
	if _, ok := nodeIndex[m.Root]; !ok {
		return fmt.Errorf("%w: root node %d missing", ErrInvalidManifest, m.Root)
	}

	const (
		unseen = iota
		visiting
		done
	)
	state := make([]uint8, len(m.Nodes))
	var visit func(NodeID, []NodeID) error
	visit = func(id NodeID, stack []NodeID) error {
		idx, ok := nodeIndex[id]
		if !ok {
			return fmt.Errorf("%w: node %d missing", ErrInvalidManifest, id)
		}
		switch state[idx] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("%w: cycle through node %d", ErrInvalidManifest, id)
		}
		for _, s := range stack {
			if s == id {
				return fmt.Errorf("%w: cycle through node %d", ErrInvalidManifest, id)
			}
		}
		state[idx] = visiting
		stack = append(stack, id)
		n := &m.Nodes[idx]
		switch n.Kind {
		case KindRaw:
			b, ok := blockIndex[n.Block]
			if !ok {
				return fmt.Errorf("%w: node %d references missing block %d", ErrInvalidManifest, id, n.Block)
			}
			n.Span = b.Size
		case KindDict:
			b, ok := blockIndex[n.Block]
			if !ok {
				return fmt.Errorf("%w: node %d references missing block %d", ErrInvalidManifest, id, n.Block)
			}
			if n.Off > b.Size || n.Length > b.Size-n.Off {
				return fmt.Errorf("%w: dict node %d slice [%d,%d) outside block %d size %d",
					ErrInvalidManifest, id, n.Off, n.Off+n.Length, n.Block, b.Size)
			}
			if n.Length > MaxExpandedLength {
				return fmt.Errorf("%w: node %d length exceeds 2^48", ErrInvalidManifest, id)
			}
			n.Span = n.Length
		case KindSlice:
			if err := visit(n.Child, stack); err != nil {
				return err
			}
			childSpan := m.Nodes[nodeIndex[n.Child]].Span
			if n.Off > childSpan || n.Length > childSpan-n.Off {
				return fmt.Errorf("%w: slice node %d [%d,%d) outside child %d length %d",
					ErrInvalidManifest, id, n.Off, n.Off+n.Length, n.Child, childSpan)
			}
			n.Span = n.Length
		case KindReverse:
			if err := visit(n.Child, stack); err != nil {
				return err
			}
			n.Span = m.Nodes[nodeIndex[n.Child]].Span
		case KindRepeat:
			if err := visit(n.Child, stack); err != nil {
				return err
			}
			childSpan := m.Nodes[nodeIndex[n.Child]].Span
			if n.Count == 0 {
				return fmt.Errorf("%w: repeat node %d has zero count", ErrInvalidManifest, id)
			}
			if childSpan == 0 {
				return fmt.Errorf("%w: repeat node %d repeats an empty child", ErrInvalidManifest, id)
			}
			if n.Count > MaxExpandedLength/childSpan {
				return fmt.Errorf("%w: repeat node %d expands beyond 2^48", ErrInvalidManifest, id)
			}
			n.Span = childSpan * n.Count
		case KindConcat:
			if len(n.Children) == 0 {
				return fmt.Errorf("%w: concat node %d has no children", ErrInvalidManifest, id)
			}
			var span uint64
			for _, cid := range n.Children {
				if err := visit(cid, stack); err != nil {
					return err
				}
				cs := m.Nodes[nodeIndex[cid]].Span
				if cs > MaxExpandedLength-span {
					return fmt.Errorf("%w: concat node %d expands beyond 2^48", ErrInvalidManifest, id)
				}
				span += cs
			}
			n.Span = span
		case KindReverseSlice:
			if err := visit(n.Child, stack); err != nil {
				return err
			}
			childSpan := m.Nodes[nodeIndex[n.Child]].Span
			if n.Off > childSpan || n.Length > childSpan-n.Off {
				return fmt.Errorf("%w: reverse-slice node %d [%d,%d) outside child %d length %d",
					ErrInvalidManifest, id, n.Off, n.Off+n.Length, n.Child, childSpan)
			}
			n.Span = n.Length
		default:
			return fmt.Errorf("%w: node %d has unknown kind %d", ErrInvalidManifest, id, n.Kind)
		}
		state[idx] = done
		return nil
	}

	for i := range m.Nodes {
		if err := visit(m.Nodes[i].ID, nil); err != nil {
			return err
		}
	}

	return nil
}

// PrepareLengths validates the DAG, fills Node.Span, and computes ContentRoot.
// It does not verify or create the signature; use Seal for that.
func (m *Manifest) PrepareLengths() error {
	if m == nil {
		return ErrInvalidManifest
	}
	if m.TotalLength > MaxExpandedLength {
		return fmt.Errorf("%w: total length %d exceeds 2^48", ErrInvalidManifest, m.TotalLength)
	}
	if err := m.prepareNodeLengths(); err != nil {
		return err
	}
	nm, err := buildNodeMap(m)
	if err != nil {
		return err
	}
	idx, ok := nm.byID[m.Root]
	if !ok {
		return fmt.Errorf("%w: root node %d missing", ErrInvalidManifest, m.Root)
	}
	rootSpan := m.Nodes[idx].Span
	if rootSpan != m.TotalLength {
		return fmt.Errorf("%w: total length %d != root span %d", ErrInvalidManifest, m.TotalLength, rootSpan)
	}
	m.ContentRoot = m.computeContentRoot()
	return nil
}

// SealFromRoot sets TotalLength from the root node's computed span, then seals
// the manifest.  It is convenient for constructing manifests where the total
// length is implied by the tree.
func (m *Manifest) SealFromRoot() error {
	if m == nil {
		return ErrInvalidManifest
	}
	if err := m.prepareNodeLengths(); err != nil {
		return err
	}
	nm, err := buildNodeMap(m)
	if err != nil {
		return err
	}
	idx, ok := nm.byID[m.Root]
	if !ok {
		return fmt.Errorf("%w: root node %d missing", ErrInvalidManifest, m.Root)
	}
	m.TotalLength = m.Nodes[idx].Span
	return m.Seal()
}

// Seal validates the manifest and creates its signature.
func (m *Manifest) Seal() error {
	if err := m.PrepareLengths(); err != nil {
		return err
	}
	m.Signature = m.ComputeSignature()
	return nil
}

// SignatureInput is the canonical byte string bound by the signature.
// It contains the version, total length, root node, content root and the
// compression-strategy digest.
func (m *Manifest) SignatureInput() []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, "ps-archive-manifest/2\x00"...)
	buf = u64Bytes(buf, m.Version)
	buf = u64Bytes(buf, m.TotalLength)
	buf = u64Bytes(buf, uint64(m.Root))
	buf = append(buf, m.ContentRoot[:]...)
	buf = append(buf, m.StrategyDigest[:]...)
	return buf
}

// ComputeSignature returns the signature for the manifest's current content.
func (m *Manifest) ComputeSignature() Digest {
	if m == nil {
		return Digest{}
	}
	// Make the operation well-defined even if the caller has not called
	// PrepareLengths first.
	if err := m.prepareNodeLengths(); err == nil {
		m.ContentRoot = m.computeContentRoot()
	}
	return sha256Digest(m.SignatureInput())
}

// VerifySignature checks the manifest signature.
func (m *Manifest) VerifySignature() error {
	if m == nil {
		return ErrInvalidManifest
	}
	// Recompute derived lengths/content root as well, so a caller cannot verify
	// a stale signature after mutating node records.
	if err := m.PrepareLengths(); err != nil {
		return err
	}
	if !m.Signature.Equal(m.ComputeSignature()) {
		return ErrBadSignature
	}
	return nil
}

// Validate validates structure and the signature, and refreshes computed
// lengths and the content root.
func (m *Manifest) Validate() error {
	return m.VerifySignature()
}

// Fingerprint returns a digest of the complete manifest, including the
// signature.  Plans record it so that a plan cannot silently be applied to a
// different manifest with the same version number.
func (m *Manifest) Fingerprint() Digest {
	buf := make([]byte, 0, 128)
	buf = append(buf, m.SignatureInput()...)
	buf = append(buf, m.Signature[:]...)
	buf = append(buf, m.ContentRoot[:]...)
	buf = append(buf, m.Strategy...)
	var b [8]byte
	for _, n := range m.Nodes {
		binary.BigEndian.PutUint64(b[:], uint64(n.ID))
		buf = append(buf, b[:]...)
		buf = append(buf, byte(n.Kind))
		binary.BigEndian.PutUint64(b[:], uint64(n.Block))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Off)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Length)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], uint64(n.Child))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Count)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], uint64(len(n.Children)))
		buf = append(buf, b[:]...)
		for _, cid := range n.Children {
			binary.BigEndian.PutUint64(b[:], uint64(cid))
			buf = append(buf, b[:]...)
		}
		binary.BigEndian.PutUint64(b[:], n.Span)
		buf = append(buf, b[:]...)
	}
	blocks := append([]Block(nil), m.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })
	for _, blk := range blocks {
		binary.BigEndian.PutUint64(b[:], uint64(blk.ID))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], blk.Size)
		buf = append(buf, b[:]...)
		buf = append(buf, blk.Codec...)
		buf = append(buf, 0)
	}
	return sha256Digest(buf)
}

func (m *Manifest) computeContentRoot() Digest {
	buf := make([]byte, 0, 128)
	buf = append(buf, "ps-archive-content/1\x00"...)
	buf = u64Bytes(buf, uint64(m.Root))
	buf = u64Bytes(buf, m.TotalLength)
	buf = append(buf, m.StrategyDigest[:]...)

	nodes := append([]Node(nil), m.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	var b [8]byte
	for _, n := range nodes {
		binary.BigEndian.PutUint64(b[:], uint64(n.ID))
		buf = append(buf, b[:]...)
		buf = append(buf, byte(n.Kind))
		binary.BigEndian.PutUint64(b[:], uint64(n.Block))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Off)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Length)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], uint64(n.Child))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], n.Count)
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], uint64(len(n.Children)))
		buf = append(buf, b[:]...)
		for _, cid := range n.Children {
			binary.BigEndian.PutUint64(b[:], uint64(cid))
			buf = append(buf, b[:]...)
		}
		binary.BigEndian.PutUint64(b[:], n.Span)
		buf = append(buf, b[:]...)
	}
	blocks := append([]Block(nil), m.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].ID < blocks[j].ID })
	for _, blk := range blocks {
		binary.BigEndian.PutUint64(b[:], uint64(blk.ID))
		buf = append(buf, b[:]...)
		binary.BigEndian.PutUint64(b[:], blk.Size)
		buf = append(buf, b[:]...)
		buf = append(buf, blk.Codec...)
		buf = append(buf, 0)
	}
	return sha256Digest(buf)
}

// nodeMap returns node lookup helpers shared by resolution and planning.
type nodeMap struct {
	byID   map[NodeID]int
	blocks map[BlockID]Block
}

func buildNodeMap(m *Manifest) (*nodeMap, error) {
	if m == nil {
		return nil, ErrInvalidManifest
	}
	nm := &nodeMap{
		byID:   make(map[NodeID]int, len(m.Nodes)),
		blocks: make(map[BlockID]Block, len(m.Blocks)),
	}
	for i, n := range m.Nodes {
		if _, dup := nm.byID[n.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate node id %d", ErrInvalidManifest, n.ID)
		}
		nm.byID[n.ID] = i
	}
	for _, b := range m.Blocks {
		if _, dup := nm.blocks[b.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate block id %d", ErrInvalidManifest, b.ID)
		}
		nm.blocks[b.ID] = b
	}
	return nm, nil
}
