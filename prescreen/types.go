package prescreen

import "crypto/ed25519"

// Action names a capability operation. Only ActionRead is admitted here.
type Action string

// ActionRead is the action bound by capabilities that may produce read tickets.
const ActionRead Action = "read"

// ObjectRef identifies one versioned archive object.
type ObjectRef struct {
	Tenant  string
	Object  string
	Version uint64
}

// ViewEdge is one signed projection edge: logical objects of From are mapped to
// physical objects of To by Map. ParentVersion binds the exact From version;
// a mismatch must be rejected rather than silently re-resolved.
type ViewEdge struct {
	From          ObjectRef
	To            ObjectRef
	ParentVersion uint64
	Map           AffineMap
}

// ViewManifest is an edge plus the issuer key and its Ed25519 signature over
// the canonical edge encoding. Signer must be a trusted root.
type ViewManifest struct {
	// Name identifies this signed edge inside a tenant's view chain requests.
	Name      string
	Edge      ViewEdge
	Signer    ed25519.PublicKey
	Signature []byte
}

// CapKind distinguishes grant capabilities from revocation capabilities.
type CapKind uint8

const (
	// CapGrant adds slices to a subject's valid grant union.
	CapGrant CapKind = 1
	// CapRevoke removes any covered physical blocks from admission.
	CapRevoke CapKind = 2
)

// Capability is a signed authorization bound to tenant, an exact object
// version, an action and a set of physical affine slices.
type Capability struct {
	Kind      CapKind
	Subject   ed25519.PublicKey
	Tenant    string
	Object    ObjectRef
	Action    Action
	Slices    []Affine
	Signer    ed25519.PublicKey
	Signature []byte
}

// ReadRequest is a batch read description. Each logical slice is pushed
// through the named view chain starting at the request object; the request is
// signed by the requesting subject.
type ReadRequest struct {
	Tenant    string
	Object    ObjectRef
	Action    Action
	Slices    []Affine
	Chain     []string
	Subject   ed25519.PublicKey
	Signature []byte
}

// Policy carries the trust anchors and the signed view manifest set against
// which a request is admitted.
type Policy struct {
	// Roots are the trusted Ed25519 public keys. Every manifest and capability
	// signature must verify under one of these, otherwise the root is missing.
	Roots []ed25519.PublicKey
	// Manifests are keyed externally by manifestKey; edges form the view DAG.
	Manifests []ViewManifest
}

// IntersectionCertificate mirrors one inclusion-exclusion term. The verifier
// recomputes the intersection from the referenced slices and compares the
// canonical affine set, dimension and cardinality, never trusting the count.
type IntersectionCertificate struct {
	Sets        []int
	Intersect   Affine
	Sign        int8
	Cardinality uint64
}

// SliceCertificate proves one requested physical affine slice is covered by
// the subject's valid grant union and avoids every valid revocation slice.
type SliceCertificate struct {
	// Physical is the exact image of one request slice through the full chain.
	Physical Affine
	// GrantSlices are the flattened valid grant slices for this object.
	GrantSlices []Affine
	// GrantTerms is the recomputable inclusion-exclusion coverage proof.
	GrantTerms []IntersectionCertificate
	// CoveredSize is |Physical|; UnionSize must equal it for coverage.
	CoveredSize uint64
	UnionSize   uint64
	// RevocationSlices are the flattened valid revocation slices checked.
	RevocationSlices []Affine
	// RevokedIntersection records every non-empty intersection found; a valid
	// admission always leaves this empty.
	RevokedIntersection []Affine
}

// ObjectTicket is the admission result for one terminal physical object.
type ObjectTicket struct {
	Object       ObjectRef
	PhysicalSets []Affine
	// DistinctBlocks is |union(PhysicalSets)|, counting aliases only once.
	DistinctBlocks uint64
	// UnionTerms proves the distinct-block cardinality algebraically.
	UnionTerms   []IntersectionCertificate
	Certificates []SliceCertificate
}

// ReadTicket never expands block numbers. It describes only the physical
// affine sets actually needed, per-object exact distinct counts, and the
// algebraic coverage certificates.
type ReadTicket struct {
	Tenant   string
	Object   ObjectRef
	Terminal ObjectRef
	Objects  []ObjectTicket
	// RequestHash binds the ticket to the exact signed request bytes.
	RequestHash []byte
}
