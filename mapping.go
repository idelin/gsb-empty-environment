package destplan

// NodeKind identifies the role of a mapping-tree node.
type NodeKind int

const (
	// NodeBranch partitions its span into children placed at fixed offsets.
	NodeBranch NodeKind = iota + 1
	// NodeLeaf maps onto a contiguous run of ciphertext blocks.
	NodeLeaf
	// NodeHole is a sparse hole: no backing block and no key.
	NodeHole
	// NodeShift reuses another subtree after translating its coordinates.
	NodeShift
)

// Child places a shared subtree at a fixed offset inside a branch node.
// Child spans must stay inside the branch span and must not overlap.
type Child struct {
	Offset uint64
	Node   *MapNode
}

// MapNode is an immutable node of the shared mapping tree. The same node may
// be referenced from many snapshots, branches and shift nodes; its local
// coordinate origin is its own span [0, Span).
type MapNode struct {
	Kind NodeKind
	// Span is the length of the node's local domain, in logical units. It is
	// 128-bit so that a node covering the whole 64-bit space (length 2^64) is
	// representable. The zero value is invalid. Use Span64 or SpanFull to
	// build it.
	Span Addr

	// Branch children (Kind == NodeBranch).
	Children []Child

	// Leaf mapping (Kind == NodeLeaf).
	// BlockBase is the identity coordinate of the first ciphertext block;
	// blocks are identified as [BlockBase, BlockBase+Span). CoordBase is the
	// storage coordinate of the first block and defaults to BlockBase when
	// zero. DataKey names the key under which every block is encrypted.
	BlockBase uint64
	CoordBase uint64
	DataKey   string

	// Shift reference (Kind == NodeShift).
	Inner *MapNode
	// Delta is added to every logical coordinate produced by Inner.
	Delta int64
}

// Span64 builds a node span of at most 2^64-1 units.
func Span64(v uint64) Addr { return Addr64(v) }

// SpanFull returns the span of the complete 64-bit logical space.
func SpanFull() Addr { return AddrEnd() }

// Snapshot is an immutable snapshot root. Its tree is shared by value: the
// root node may be reused across snapshots, and Shift translates the whole
// tree into this snapshot's logical coordinates.
type Snapshot struct {
	ID    string
	Root  *MapNode
	Shift int64
}

// Requirement attaches a half-open logical interval to a snapshot.
type Requirement struct {
	Snapshot string
	Interval Interval
}

// Piece is one symbolic result of resolving an interval against a snapshot:
// the backing block-identity range and storage-coordinate range, together
// with the data key that encrypts them. Ranges stay symbolic; no per-sector
// expansion is ever performed.
type Piece struct {
	LogLo   Addr
	LogHi   Addr
	BlockLo uint64
	BlockHi Addr
	CoordLo uint64
	CoordHi Addr
	Key     string
}

// Gap records a requested span that has no backing storage: either a sparse
// hole inside the tree or an address past the snapshot's mapped domain.
type Gap struct {
	LogLo Addr
	LogHi Addr
	Hole  bool
}

// Resolution is the symbolic expansion of one requested interval.
type Resolution struct {
	Pieces []Piece
	Gaps   []Gap
}

// KeyRole marks root keys and data keys in the wrapping graph.
type KeyRole int

const (
	// KeyRoot is available to the volume host without any unwrapping.
	KeyRoot KeyRole = iota + 1
	// KeyData encrypts ciphertext blocks referenced by mapping leaves.
	KeyData
	// KeyIntermediate is an internal wrapping key reachable only via edges.
	KeyIntermediate
)

// WrapEdge is a directed wrapping edge from key From to key To. A root key
// can unwrap To by following a surviving path of edges. Aliases name the
// same physical wrapping edge and are always deactivated together.
type WrapEdge struct {
	ID      string
	From    string
	To      string
	Cost    uint64
	Aliases []string
}

// KeyNode declares a key and its role.
type KeyNode struct {
	ID   string
	Role KeyRole
}

// KeyGraph is the directed key-wrapping graph shared by all snapshots.
type KeyGraph struct {
	Keys  []KeyNode
	Edges []WrapEdge
}

// Problem is the full input of a destruction-planning request.
type Problem struct {
	Snapshots []Snapshot
	Graph     KeyGraph
	// Destroy intervals: every backing block must lose all root paths.
	Destroy []Requirement
	// Retain intervals: every backing block must keep at least one root path.
	Retain []Requirement
}
