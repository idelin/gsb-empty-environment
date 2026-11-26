package archiveproof

import (
	"errors"
	"fmt"
	"math"
)

// CertificateVersion is the on-disk certificate format version.
const CertificateVersion = 1

// NodeID identifies an evidence record.  Hidden record identities must never
// be copied into a certificate.
type NodeID string

// ServiceID is a service's permanent identity.  Rotating KeyIDs beneath the
// same ServiceID does not create a new clock source.
type ServiceID string

// KeyID identifies one signing key of a service.  Keys are expected to be
// rotated; KeyID is deliberately not used for clock identity.
type KeyID string

// Interval is a closed integer interval.  It is used for signed clock-offset
// bounds and for signed bounds on the real-time difference between a renewal
// and the record it covers.
type Interval struct {
	Lo int64 `json:"lo"`
	Hi int64 `json:"hi"`
}

// Offset is a signed bound on a clock source's offset.
type Offset = Interval

// Gap is a signed bound on real(renewer)-real(renewed).
type Gap = Interval

// Exact returns the interval [v, v].
func Exact(v int64) Interval { return Interval{Lo: v, Hi: v} }

// Between returns the inclusive interval [lo, hi].
func Between(lo, hi int64) Interval { return Interval{Lo: lo, Hi: hi} }

// AtLeast returns [lo, +infinity].
func AtLeast(lo int64) Interval { return Interval{Lo: lo, Hi: math.MaxInt64} }

// AtMost returns [-infinity, hi].
func AtMost(hi int64) Interval { return Interval{Lo: math.MinInt64, Hi: hi} }

// AllTime returns the unbounded interval.
func AllTime() Interval { return Interval{Lo: math.MinInt64, Hi: math.MaxInt64} }

// Valid reports whether the interval is non-empty.
func (i Interval) Valid() bool { return i.Lo <= i.Hi }

// Contains reports whether v is in i.
func (i Interval) Contains(v int64) bool { return i.Lo <= v && v <= i.Hi }

// ContainsInterval reports whether every value in other is in i.
func (i Interval) ContainsInterval(other Interval) bool {
	return other.Valid() && i.Lo <= other.Lo && other.Hi <= i.Hi
}

// Intersect returns the intersection of i and other.
func (i Interval) Intersect(other Interval) (Interval, bool) {
	out := Interval{Lo: max64(i.Lo, other.Lo), Hi: min64(i.Hi, other.Hi)}
	return out, out.Valid()
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Reference names an evidence entry either by a visible node ID or by an
// opaque commitment.  Exactly one field is non-zero.
type Reference struct {
	Node       NodeID     `json:"node,omitempty"`
	Commitment Commitment `json:"commitment,omitempty"`
}

// RefNode returns a reference to a visible node.
func RefNode(id NodeID) Reference { return Reference{Node: id} }

// RefCommitment returns a reference to an opaque evidence commitment.
func RefCommitment(c Commitment) Reference { return Reference{Commitment: c} }

// Validate checks that the reference is unambiguous.
func (r Reference) Validate() error {
	hasNode := r.Node != ""
	hasCommitment := !r.Commitment.IsZero()
	if hasNode == hasCommitment {
		return fmt.Errorf("%w: reference must name exactly one node or commitment", ErrInvalidReference)
	}
	return nil
}

func (r Reference) key() string {
	if r.Node != "" {
		return "n:" + string(r.Node)
	}
	return "c:" + r.Commitment.String()
}

// Renewal is a signed statement by a newer record about the older record it
// actually covers.  For a visible old record both OldID and OldContent are
// mandatory.  For an opaque old record only OldCommitment may be disclosed.
type Renewal struct {
	OldID         NodeID     `json:"old_id,omitempty"`
	OldContent    Digest     `json:"old_content,omitempty"`
	OldCommitment Commitment `json:"old_commitment,omitempty"`
	Gap           Gap        `json:"gap"`
}

// CommitmentLink is one of the possibly many evidence commitments signed by a
// single timestamp.  The gap bounds the real-time difference between the
// signing record and the committed evidence.
type CommitmentLink struct {
	Commitment Commitment `json:"commitment"`
	Gap        Gap        `json:"gap"`
}

// SharedRenewal is a renewal edge between two opaque shared evidence entries.
type SharedRenewal struct {
	To  Commitment `json:"to"`
	Gap Gap        `json:"gap"`
}

// Node is one evidence record in the archive host's database.
type Node struct {
	ID      NodeID    `json:"id"`
	Service ServiceID `json:"service"`
	Key     KeyID     `json:"key,omitempty"`

	// Source is the permanent clock-source identity.  If empty it defaults to
	// Service.  Key rotation therefore does not create a new clock source.
	Source ServiceID `json:"source,omitempty"`

	Time    int64            `json:"time"`
	Content Digest           `json:"content"`
	Offset  Offset           `json:"offset"`
	Commits []CommitmentLink `json:"commits,omitempty"`
	Renews  []Renewal        `json:"renews,omitempty"`

	// Approved records may be read by the archive verifier.  Unapproved
	// records remain hidden and must not be copied into a certificate.
	Approved bool `json:"approved"`

	// Commitment is an optional explicit record commitment.  When zero,
	// NodeCommitment derives one from Service and Content.
	Commitment Commitment `json:"commitment,omitempty"`
}

// ClockSource returns the permanent clock source for a record.
func (n Node) ClockSource() ServiceID {
	if n.Source != "" {
		return n.Source
	}
	return n.Service
}

// NodeCommitment returns the explicit commitment, or derives one.
func (n Node) NodeCommitment() Commitment {
	if !n.Commitment.IsZero() {
		return n.Commitment
	}
	return CommitmentOf(n.Service, n.Content)
}

// SharedEvidence is a licensed (or, inside the host, unlicensed) opaque
// commitment that may stand for one or more hidden evidence records.  All
// records represented by one SharedEvidence share the timestamp, clock source,
// offset bound and continuity edges.  This is precisely the "many early
// records share a timestamp and migration credential" case.
type SharedEvidence struct {
	Commitment Commitment `json:"commitment"`
	Time       int64      `json:"time"`
	Source     ServiceID  `json:"source"`
	Offset     Offset     `json:"offset"`

	// Covers lists the hidden record identities represented by this shared
	// commitment.  It is archive-side metadata and is stripped from every
	// certificate.
	Covers []NodeID `json:"covers,omitempty"`

	RenewsNodes  []Renewal       `json:"renews_nodes,omitempty"`
	RenewsShared []SharedRenewal `json:"renews_shared,omitempty"`

	// A non-empty LicensedBy means an approved timestamp signed this
	// commitment.  LicenseProof is an opaque host-supplied proof object.
	LicensedBy   NodeID `json:"licensed_by,omitempty"`
	LicenseProof []byte `json:"license_proof,omitempty"`
}

// Archive is the host-side input.  Nodes with Approved=false and unlicensed
// Shared entries are visible to the host but are never disclosed by the
// verifier-facing certificate.
type Archive struct {
	Nodes      []Node           `json:"nodes"`
	Shared     []SharedEvidence `json:"shared,omitempty"`
	Genesis    []Reference      `json:"genesis"`
	Acceptance []Reference      `json:"acceptance"`
}

// Clocking is one concrete assignment of offsets to permanent clock sources.
// Every record that references a source uses the single value in this map,
// regardless of which signing key issued it.
type Clocking map[ServiceID]int64

// Clone returns a copy of c.
func (c Clocking) Clone() Clocking {
	out := make(Clocking, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// Entry is one unit of a fixed certificate: either a fully disclosed approved
// node or an opaque, licensed shared commitment.
type Entry struct {
	Node   *Node           `json:"node,omitempty"`
	Shared *SharedEvidence `json:"shared,omitempty"`
}

// Certificate is a fixed, pre-issued continuity certificate.  A verifier may
// choose one chain inside it for the actual clocking, but may not fetch any
// evidence outside these entries.
type Certificate struct {
	Version       int                  `json:"version"`
	Entries       []Entry              `json:"entries"`
	Genesis       []Reference          `json:"genesis"`
	Acceptance    []Reference          `json:"acceptance"`
	Sources       map[ServiceID]Offset `json:"sources,omitempty"`
	EvidenceCount int                  `json:"evidence_count"`
}

// Chain is one continuity path from an acceptance record to an original
// signing record.  Path[0] is the acceptance and the last element is genesis.
type Chain struct {
	Acceptance Reference   `json:"acceptance"`
	Genesis    Reference   `json:"genesis"`
	Path       []Reference `json:"path"`
}

// Proof is the result of evaluating a fixed certificate under one clocking.
type Proof struct {
	Clocking Clocking `json:"clocking"`
	Chains   []Chain  `json:"chains"`
}

// Sentinel errors make host integration and tests straightforward.
var (
	ErrInvalidArchive     = errors.New("archiveproof: invalid archive")
	ErrInvalidCertificate = errors.New("archiveproof: invalid certificate")
	ErrInvalidReference   = errors.New("archiveproof: invalid reference")
	ErrUnknownTarget      = errors.New("archiveproof: unknown renewal target")
	ErrUnboundRenewal     = errors.New("archiveproof: renewal does not bind the covered record and digest")
	ErrIncompatibleClocks = errors.New("archiveproof: incompatible offsets for one clock source")
	ErrClockOutOfRange    = errors.New("archiveproof: clocking is outside the signed offset bounds")
	ErrMissingClocking    = errors.New("archiveproof: clocking does not assign every clock source")
	ErrNoCertificate      = errors.New("archiveproof: no permitted certificate proves continuity")
	ErrDisallowedEvidence = errors.New("archiveproof: evidence is not approved or licensed")
	ErrBrokenCertificate  = errors.New("archiveproof: certificate does not prove continuity")
)
