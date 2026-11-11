// Package policyeval evaluates certificate-policy constraints over
// signature-verified candidate certificate chains.
//
// A trusted chain only proves a signature path; it does not prove that the
// leaf's usage is permitted by the enterprise's certificate policy. This
// package answers the policy question independently for every candidate
// chain: starting from the policies required by the leaf's usage, walk the
// declared adjacent-layer policy mappings in reverse until the trust anchor
// layer is reached. The result is the exhaustive set of root policies that
// can legitimately authorize the requested usage, together with the complete
// mapping paths that justify each of them.
//
// The finite policy OID universe makes exhaustive enumeration tractable:
// every admissible policy path is enumerated, so cross-chain mixing, mapping
// rings and attempted splicing of two independently-failing chains can all be
// checked explicitly rather than approximated.
package policyeval

// OID is a dotted-decimal object identifier naming a certificate policy.
//
// The special OID AnyPolicy is the wildcard policy (id-ce-certificatePolicies
// anyPolicy in X.509). As a concrete carried policy it only has meaning at a
// layer that explicitly lists it; it is never synthesized during evaluation.
type OID string

// AnyPolicy is the anyPolicy wildcard OID.
const AnyPolicy OID = "2.5.29.32.0"

// Mapping is one entry of a certificate's policyMappings extension.
//
// IssuerDomain is the policy as named in the issuer certificate one layer
// closer to the root; SubjectDomain is the equivalent policy in the
// certificate that carries the mapping (the subject-side layer). Following
// X.509 semantics the mapping lives on the subject-side layer and is only
// ever applied across the single adjacent boundary at which it is declared.
type Mapping struct {
	IssuerDomain  OID
	SubjectDomain OID
}

// Layer is one certificate in a chain, ordered from the trust anchor at
// index 0 to the leaf certificate at the last index.
type Layer struct {
	// Name is an optional human-readable label used in diagnostics.
	Name string

	// AllowedPolicies is the certificate's certificatePolicies extension:
	// the full set of policy OIDs that are permitted at this layer. The
	// set must contain at least one entry; anyPolicy may appear as a
	// wildcard entry.
	AllowedPolicies []OID

	// Mappings are the policy mappings declared at the boundary between
	// this layer and the adjacent layer closer to the root: a mapping here
	// translates a policy in the upper (issuer) layer into one of this
	// layer's policies. For the trust-anchor layer there is no upper
	// boundary, so its Mappings must be empty.
	Mappings []Mapping

	// InhibitMapping marks this layer's upper boundary as policy-mapping
	// inhibited. When true, every mapping declared by this layer is ignored
	// and only the identity relation (a policy OID carrying unchanged from
	// the issuer layer) may cross that boundary.
	InhibitMapping bool
}

// Chain is one signature-verified candidate chain together with the policy
// inputs attached to each of its layers. Signature verification is assumed to
// have already succeeded; this package never touches certificates or keys.
type Chain struct {
	// ID identifies the candidate chain in reports and diagnostics.
	ID string

	// Layers runs from the trust anchor (index 0) to the leaf (last index).
	Layers []Layer
}

// PathStep records one layer traversal in a fully justified mapping path.
type PathStep struct {
	// Layer is the index of the layer at which Policy holds.
	Layer int

	// LayerName is the optional label of that layer.
	LayerName string

	// Policy is the policy OID active at this layer along the path.
	Policy OID

	// AppliedMapping is the mapping crossed at the boundary immediately
	// above this layer (between this layer and the previous step). It is
	// nil for the identity relation and for the trust-anchor step, which
	// has no boundary above it.
	AppliedMapping *Mapping

	// Inhibited reports that the boundary immediately above this layer was
	// policy-mapping inhibited, so this step is reachable only via the
	// identity relation.
	Inhibited bool
}

// Path is one complete, fully justified policy path from a root policy down
// to a policy required by the leaf usage.
type Path struct {
	// Steps runs from the trust-anchor layer (index 0) to the leaf layer.
	Steps []PathStep

	// RootPolicy is the policy at the trust-anchor layer of this path.
	RootPolicy OID

	// LeafPolicy is the leaf-usage policy reached at the end of this path.
	LeafPolicy OID
}

// Decision classifies the outcome of evaluating a single candidate chain.
type Decision string

const (
	// DecisionAccepted means at least one root policy authorizes the
	// requested leaf usage along a fully justified path.
	DecisionAccepted Decision = "accepted"

	// DecisionRejected means no root policy can authorize the requested
	// leaf usage under the declared policy sets, mappings and inhibit
	// markers. An empty intermediate or root policy set always rejects.
	DecisionRejected Decision = "rejected"
)

// Result is the policy evaluation outcome for one candidate chain.
type Result struct {
	// ChainID identifies the evaluated candidate chain.
	ChainID string

	// Decision is accepted or rejected.
	Decision Decision

	// Reason carries a human-readable explanation for a rejection and is
	// empty for an acceptance.
	Reason string

	// RootPolicies is the exhaustive, de-duplicated, deterministically
	// ordered set of acceptable root policies. It is empty when the chain
	// is rejected.
	RootPolicies []OID

	// Paths is the exhaustive set of complete mapping paths, one per
	// admissible root-to-leaf policy derivation. Each listed root policy is
	// supported by at least one path here.
	Paths []Path
}
