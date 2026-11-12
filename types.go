package netsec

import "errors"

// Sentinel errors returned by Solve.
var (
	// ErrEmptyInput is returned when the solver is invoked without a usable input.
	ErrEmptyInput = errors.New("netsec: empty input")

	// ErrUnknownReference is returned when a signature, certificate, control
	// edge or threshold points at an organization or certificate that was
	// never declared.
	ErrUnknownReference = errors.New("netsec: unknown reference")

	// ErrDuplicateSignature is returned when two witness entries share a
	// SignatureID. One signature is one witness and must be declared once.
	ErrDuplicateSignature = errors.New("netsec: duplicate signature id")

	// ErrNoQualifiedSet is returned when no witness subset satisfies the
	// thresholds, including the case where every qualified subset would
	// contain more witnesses than MaxWitnesses, or when the enumeration
	// budget is exhausted.
	ErrNoQualifiedSet = errors.New("netsec: no qualified witness set")
)

// hardWitnessCap is the protocol limit on the size of a qualified witness set.
const hardWitnessCap = 20

// enumBudget caps the number of subset-enumeration nodes visited by one Solve
// call. It keeps the solver bounded on pathological inputs; ordinary cases
// terminate much earlier because each control domain contributes at most one
// witness.
const enumBudget = 2_000_000

// Organization is a legal entity that may hold certificates.
type Organization struct {
	// ID is the unique identifier referenced by certificates and control edges.
	ID string
	// Region is the regulatory region the organization belongs to.
	Region string
	// Category is the kind of organization, e.g. "ca" or "notary".
	Category string
}

// Certificate is a signing credential held by one organization. Multiple
// certificates held by the same organization (or by organizations inside the
// same control domain) are certificate aliases and must never be counted twice.
type Certificate struct {
	// ID is the unique certificate identifier.
	ID string
	// Holder is the Organization.ID holding this certificate.
	Holder string
}

// ControlEdge declares that From controls To. Control is transitive: an
// organization controls every organization reachable through control edges.
// Organizations linked by any directed control path share one control domain
// and count as a single independent party.
type ControlEdge struct {
	From string
	To   string
}

// Signature is one witnessed artifact signature.
type Signature struct {
	// SignatureID uniquely identifies the witness signature.
	SignatureID string
	// CertificateID is the certificate that produced the signature.
	CertificateID string
}

// Thresholds declares the region/category combination requirements. Every
// field is optional: a zero value disables that constraint. A MinPairs entry
// keyed "region|category" requires at least that many witnesses whose
// organization sits in the given region and has the given category.
type Thresholds struct {
	// MinWitnesses is the minimum total number of independent witnesses.
	MinWitnesses int
	// MinRegions requires witnesses from at least that many distinct regions.
	MinRegions int
	// MinCategories requires witnesses from at least that many categories.
	MinCategories int
	// MinPairs requires specific region+category combinations.
	MinPairs map[string]int
}

// Input bundles everything the solver needs.
type Input struct {
	Organizations []Organization
	Certificates  []Certificate
	ControlEdges  []ControlEdge
	Signatures    []Signature
	Thresholds    Thresholds
	// MaxWitnesses bounds the returned set size. Non-positive values default
	// to the protocol cap of 20; values above 20 are rejected.
	MaxWitnesses int
}

// Witness is one entry of a qualified witness set, attributed to the
// organization and control domain that actually counts.
type Witness struct {
	SignatureID   string
	CertificateID string
	Organization  string
	Region        string
	Category      string
	ControlDomain string
}

// Result is the solver output.
type Result struct {
	// Witnesses is the smallest qualified witness set, sorted by SignatureID.
	// It is empty (but non-nil) when the thresholds demand nothing.
	Witnesses []Witness
	// ControlDomain maps every organization ID to its control-domain ID.
	ControlDomain map[string]string
	// EnumVisited is the number of subset-enumeration nodes consumed.
	EnumVisited int
}
