package readproof

// NodeKind distinguishes manifest leaves from concatenation nodes.
type NodeKind uint8

const (
	// LeafNode maps a logical interval to a physical block.
	LeafNode NodeKind = iota
	// InternalNode is an ordered concatenation of child subintervals.
	InternalNode
)

// Leaf is a physical block number and its logical byte length.
type Leaf struct {
	Block  uint64
	Length uint64
}

// Child is a half-open interval [Offset, Offset+Length) of a child node's
// logical byte space.  Length zero is a valid, empty concatenation element.
type Child struct {
	Digest Digest
	Offset uint64
	Length uint64
}

// Node is an authenticated manifest node.
//
// For LeafNode, Leaf is meaningful and Children must be empty.  For
// InternalNode, Children is meaningful and Leaf is ignored.
type Node struct {
	Kind     NodeKind
	Leaf     Leaf
	Children []Child
}

// NewLeaf constructs a leaf node.
func NewLeaf(block, length uint64) Node {
	return Node{Kind: LeafNode, Leaf: Leaf{Block: block, Length: length}}
}

// NewInternal constructs an ordered concatenation node.
func NewInternal(children ...Child) Node {
	return Node{Kind: InternalNode, Children: append([]Child(nil), children...)}
}

// FullRef references the complete logical content of a child.
func FullRef(digest Digest, length uint64) Child {
	return Child{Digest: digest, Length: length}
}

// IntervalRef references [offset, offset+length) of a child.
func IntervalRef(digest Digest, offset, length uint64) Child {
	return Child{Digest: digest, Offset: offset, Length: length}
}

// ByteRange is a half-open interval in root logical coordinates.
type ByteRange struct {
	Start uint64
	End   uint64
}

// NewRange constructs a half-open range.
func NewRange(start, end uint64) ByteRange {
	return ByteRange{Start: start, End: end}
}

// Valid reports whether Start <= End.
func (r ByteRange) Valid() bool {
	return r.Start <= r.End
}

// Length returns End-Start.  It is zero for an invalid range.
func (r ByteRange) Length() uint64 {
	if !r.Valid() {
		return 0
	}
	return r.End - r.Start
}

// Contains reports whether [start, end) is inside r.
func (r ByteRange) Contains(start, end uint64) bool {
	return r.Valid() && start >= r.Start && end <= r.End && start <= end
}

// PhysicalRange is a half-open byte interval in one physical block.
type PhysicalRange struct {
	Block  uint64
	Offset uint64
	Length uint64
}

// End returns Offset+Length and whether the addition did not overflow.
func (r PhysicalRange) End() (uint64, bool) {
	end := r.Offset + r.Length
	return end, end >= r.Offset
}

// PathRef is one step on a root-to-leaf path.  Offset is relative to the
// referenced node's own logical byte space and Length is the selected byte
// count at that level.
type PathRef struct {
	Digest Digest
	Offset uint64
	Length uint64
}

// Segment is one leaf-local piece of a read certificate.
type Segment struct {
	Path         []PathRef
	Range        ByteRange
	Physical     PhysicalRange
	CredentialID string
	Generation   uint64
	Validated    PhysicalRange
}

// Certificate proves exact coverage of Range for one tenant and root.
type Certificate struct {
	Root     Digest
	Tenant   string
	Range    ByteRange
	Segments []Segment
}

// SignedRoot is a root identity and its opaque signature.
type SignedRoot struct {
	Digest    Digest
	Signature []byte
}

// VerifyRootFunc is the injected root signature verifier.  It must return nil
// only for a signature accepted for digest.
type VerifyRootFunc func(digest Digest, signature []byte) error

// Credential authorizes one tenant to read Range of Root at Generation.
// Range is always interpreted in root coordinates.
type Credential struct {
	ID         string
	Tenant     string
	Root       Digest
	Range      ByteRange
	Generation uint64
}

// Revocation invalidates all generations of CredentialID up to and including
// Generation.  If AllGenerations is true, even later generations are invalid.
type Revocation struct {
	CredentialID   string
	Generation     uint64
	AllGenerations bool
}

// AuthorizationView is the reader's current, complete authorization snapshot.
// It is a value, not an accumulator: credentials absent from this value are
// not trusted.
type AuthorizationView struct {
	Tenant      string
	Root        Digest
	Credentials []Credential
	Revocations []Revocation
}

// Request contains all evidence and policy inputs for issuing or accepting a
// certificate.
//
// MaxSegments limits the number of output segments.  Zero or a negative value
// means no explicit segment limit.
type Request struct {
	Root        SignedRoot
	View        AuthorizationView
	Range       ByteRange
	Validated   []PhysicalRange
	MaxSegments int
}
