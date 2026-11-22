package proofaccept

// EventID identifies one operation in the causal history.
type EventID string

// PageID is a virtual page name used by a measurement or operation.
type PageID string

// FrameID is the underlying page/object frame.  Virtual aliases that share a
// FrameID describe the same page.
type FrameID string

// ObjectID identifies the immutable object backing a mapping.
type ObjectID string

// SignerID identifies the source that signed an object or mapping.
type SignerID string

// Digest is an opaque content digest.  It is compared as part of a mapping,
// never as a complete mapping identity by itself.
type Digest string

// Generation identifies one mapping generation.  A remap or content change
// must advance or replace the generation even when the digest is unchanged.
type Generation uint64

// SampleID identifies one local measurement.
type SampleID string

// Operation is an operation type in the causal history.
type Operation string

const (
	// OpMap installs a mapping.
	OpMap Operation = "map"
	// OpModify changes the mapped content and therefore its generation/digest.
	OpModify Operation = "modify"
	// OpRemap installs a new mapping generation.
	OpRemap Operation = "remap"
	// OpRevoke removes a page mapping and/or revokes an object's trust.
	// Object revocation is sticky: later map events for the same object do
	// not make it trusted again.
	OpRevoke Operation = "revoke"
)

// PageState is the complete state of one page mapping at an instant.
//
// Page is a virtual alias and is retained only for diagnostics.  Frame is the
// canonical backing page.  When Frame is empty, Input.Aliases (or the Page
// value if there is no alias entry) determines the backing page.
type PageState struct {
	Page       PageID
	Frame      FrameID
	Object     ObjectID
	Generation Generation
	Digest     Digest
	Executable bool
	Signer     SignerID
}

// Event is one operation in the causal DAG.  Map, modify, and remap events use
// Page/Frame and the mapping fields.  A revoke event with Page/Frame unmaps
// that page; a revoke event with Object makes that object permanently
// untrusted from the cut that includes the revoke onward.  Both effects may be
// present on one revoke event.
type Event struct {
	ID EventID
	Op Operation

	Page  PageID
	Frame FrameID

	Object     ObjectID
	Generation Generation
	Digest     Digest
	Executable bool
	Signer     SignerID

	// Trusted asserts that this mapping's object/signature source is trusted
	// when this event is included.  A revoke always overrides it.
	Trusted bool

	Deps []EventID
}

// Sample is one locally consistent page-set measurement.
//
// States must all hold at one instant inside the sample's interval.  After
// lists events that must already be included at that instant; Before lists
// events that must not yet be included.  When more than one sample is checked,
// one cut must satisfy the union of those interval constraints.
//
// IncompletePages names pages for which the host did not receive a complete
// change list.  Observations of those pages are not used to prove a conflict;
// if they are needed for a conclusion the result is evidence insufficient,
// never inconsistent.
type Sample struct {
	ID     SampleID
	States []PageState

	After  []EventID
	Before []EventID

	IncompletePages []PageID
}

// Input is a complete verification request.  TrustedObjects establishes
// initial object trust.  Trust may also be established by Event.Trusted.
type Input struct {
	Events  []Event
	Samples []Sample

	// Aliases maps virtual page IDs to their backing frames.
	Aliases map[PageID]FrameID

	// Initial contains states that exist before the first event.  Their Frame
	// (or alias/home page) is the canonical key.
	Initial []PageState

	// TrustedObjects is initial trust; absence means untrusted.
	TrustedObjects map[ObjectID]bool
}

// Status is the conclusion of a check.
type Status string

const (
	// StatusCompatible means a common prefix exists and every executable page
	// in every compatible prefix comes from a currently trusted object.
	StatusCompatible Status = "compatible"
	// StatusIncompatible means no common prefix exists.
	StatusIncompatible Status = "incompatible"
	// StatusEvidenceInsufficient means at least one observed page lacked a
	// complete change list, so the available evidence cannot settle the issue.
	StatusEvidenceInsufficient Status = "evidence_insufficient"
	// StatusPossiblyUnsafe means compatible prefixes exist, but at least one is
	// fully trusted and at least one is not.
	StatusPossiblyUnsafe Status = "possibly_unsafe"
	// StatusUnsafe means compatible prefixes exist, but none is fully trusted.
	StatusUnsafe Status = "unsafe"
	// StatusInvalidInput means the history or certificate is malformed.
	StatusInvalidInput Status = "invalid_input"
	// StatusCanceled means the caller canceled or the deadline expired.
	StatusCanceled Status = "canceled"
)

// Result is returned by Check and Accept.
type Result struct {
	Status Status

	// CommonPrefix is true when at least one legal causal cut satisfies all
	// complete observations.
	CommonPrefix bool

	// Safe is true when every executable page at the returned witness cut is
	// backed by a currently trusted object.  For StatusCompatible it is true;
	// for StatusPossiblyUnsafe or StatusUnsafe it is false.
	Safe bool

	// Frontier is a compact cut witness: the maximal included events.  Events
	// in its predecessor closure are included and all other events are not.
	Frontier []EventID

	// Conflict is a minimum-cardinality incompatible sample set, ordered
	// lexicographically.  It is set only for StatusIncompatible.
	Conflict []SampleID

	// Incomplete lists canonical pages whose change lists were incomplete.
	Incomplete []PageID

	// CertificateAccepted is meaningful only for Accept and its wrappers.
	CertificateAccepted bool

	Reason string
}

// CertificateKind selects the kind of independently verifiable proof.
type CertificateKind string

const (
	// FrontierCertificate proves a common prefix by naming a cut frontier.
	FrontierCertificate CertificateKind = "frontier"
	// ConflictCertificate proves incompatibility by naming a minimum sample
	// set that cannot share a prefix.
	ConflictCertificate CertificateKind = "conflict"
)

// Certificate is an independently verifiable frontier or conflict proof.
type Certificate struct {
	Kind     CertificateKind
	Frontier []EventID
	Samples  []SampleID
}
