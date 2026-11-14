// Package aspa implements RPKI ASPA (Autonomous System Provider
// Authorization) path verification for a BGP decision process.
//
// The verifier consumes AS_PATH segments in received order, the
// neighbor relationship, the address family, and a pinned RPKI
// snapshot of customer-to-provider authorizations. It normalizes the
// path per RFC conventions, then checks consecutive AS pairs along
// the up-ramp and down-ramp, detecting valleys and route leaks.
package aspa

import "time"

// ASN is a 32-bit autonomous system number (4-byte ASN only, as
// required by the ASPA profile).
type ASN uint32

// SegmentType identifies the AS_PATH segment encoding.
type SegmentType int

const (
	SegSequence SegmentType = iota
	SegSet
	SegConfedSequence
	SegConfedSet
)

// Segment is one AS_PATH segment as received. ASNs are listed in
// received order: ASNs[0] is the most recently added (closest to the
// local AS).
type Segment struct {
	Type SegmentType
	ASNs []ASN
}

// AFI identifies the address family the route was received for.
type AFI uint16

const (
	AFIIPv4 AFI = 1
	AFIIPv6 AFI = 2
)

// Direction is the propagation direction used for verification.
type Direction int

const (
	// Upstream verifies a route received from a customer, peer, or
	// route server: pairs are checked customer-to-provider moving
	// away from the local AS.
	Upstream Direction = iota
	// Downstream verifies a route received from a provider.
	Downstream
)

func (d Direction) String() string {
	if d == Downstream {
		return "downstream"
	}
	return "upstream"
}

// NeighborRelation describes the BGP session the route arrived on.
type NeighborRelation int

const (
	RelationCustomer NeighborRelation = iota
	RelationPeer
	RelationProvider
	RelationRouteServer
)

// DirectionForRelation maps a neighbor relationship to the
// verification direction: routes from a provider are verified as
// downstream, everything else as upstream.
func DirectionForRelation(r NeighborRelation) Direction {
	if r == RelationProvider {
		return Downstream
	}
	return Upstream
}

// State is the ASPA verification outcome.
type State int

const (
	StateValid State = iota
	StateInvalid
	StateUnknown
)

func (s State) String() string {
	switch s {
	case StateValid:
		return "Valid"
	case StateInvalid:
		return "Invalid"
	default:
		return "Unknown"
	}
}

// ASPA is one customer-to-provider authorization record from the
// RPKI. NotAfter is the object expiry; zero means no expiry.
type ASPA struct {
	Customer  ASN
	Providers []ASN
	NotAfter  time.Time
}

// Conflict describes the first AS pair that failed verification.
type Conflict struct {
	// PairIndex is the index into the normalized sequence; the
	// failing pair is (seq[PairIndex], seq[PairIndex+1]).
	PairIndex int
	// Customer is the AS whose ASPA record was expected to
	// authorize the pair.
	Customer ASN
	// Provider is the AS that was not authorized as a provider.
	Provider ASN
	// ASPA is the record consulted; nil when no record exists for
	// Customer (which yields Unknown rather than Invalid).
	ASPA *ASPA
}

// Result is the outcome of verifying one path against one snapshot.
type Result struct {
	State     State
	Direction Direction
	AFI       AFI
	// Serial is the snapshot serial the path was verified against;
	// a single verification never mixes objects from two serials.
	Serial uint64
	// Conflict is set for Invalid results, and for Unknown results
	// caused by a missing ASPA record.
	Conflict *Conflict
	Reason   string
}
