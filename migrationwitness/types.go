package migrationwitness

// KeyID identifies a signing key known to a TrustStore.
type KeyID string

// Digest is an opaque legacy digest. Legacy digests are explicitly assumed to
// be collision-prone: distinct objects may share the same Digest value.
type Digest string

// NodeID identifies a node in the signed manifest graph. Candidate identities
// are node identities: every migratable object is a manifest node.
type NodeID string

// CandidateID identifies a candidate object bound by a disambiguation receipt.
// Its value space is the node identity space.
type CandidateID string

// ReceiptID identifies a disambiguation receipt so that it can be withdrawn.
type ReceiptID string

// NodeKind classifies a manifest node.
type NodeKind byte

const (
	// KindUnknown is the zero value and never appears in a valid snapshot.
	KindUnknown NodeKind = 0
	// KindBlob is a raw block node.
	KindBlob NodeKind = 1
	// KindConcat is an ordered concatenation node.
	KindConcat NodeKind = 2
)

// Ref is an edge target in the legacy graph. It deliberately contains only a
// collision-prone old digest and the expected logical length, never content.
type Ref struct {
	Digest Digest
	Length int64
}

// BlobStatement is a signed raw block. Several blob statements may declare the
// same Ref while carrying different payloads; that is a legacy collision.
type BlobStatement struct {
	Node    NodeID
	Ref     Ref
	Payload []byte
}

// ConcatStatement is a signed node that concatenates ordered edge references.
// Ref.Length is the full logical length and must equal the sum of the resolved
// edge lengths.
type ConcatStatement struct {
	Node  NodeID
	Ref   Ref
	Edges []Ref
}

// AliasStatement redirects one legacy name to another. Aliases may form chains;
// an alias cycle is illegal for the branch that enters it but never invalidates
// independent branches.
type AliasStatement struct {
	From Ref
	To   Ref
}

// EdgeKey identifies a single outgoing edge position of a manifest node. The
// same identified node reached through a shared subgraph must make the same
// choice on the same edge throughout one resolution.
type EdgeKey struct {
	Manifest NodeID
	Edge     int
}

// DisambiguationReceipt pins one manifest edge to one candidate identity.
type DisambiguationReceipt struct {
	ID        ReceiptID
	Manifest  NodeID
	Edge      int
	Candidate CandidateID
}

// ClosureReceipt declares that the candidate table will not gain any further
// blob, concat or alias statements.
type ClosureReceipt struct {
	// Scope is reserved for future per-table closures; the empty string denotes
	// the single global candidate table used by this package.
	Scope string
}

// WithdrawalStatement retracts a previously accepted disambiguation receipt.
type WithdrawalStatement struct {
	Receipt ReceiptID
}
