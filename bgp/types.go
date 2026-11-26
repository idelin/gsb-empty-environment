// Package bgp implements a deterministic BGP-4 best-path selection engine and
// an RFC 4456 route-reflector advertisement generator.
//
// The package models the data-plane-independent inputs of a single prefix's
// candidate path table and produces:
//
//   - a legality-filtered, arrival-order-independent best path, and
//   - the per-peer advertisements an RR / confederation member would emit,
//     including ORIGINATOR_ID and CLUSTER_LIST rewriting.
//
// Confederation segments (AS_CONFED_SEQUENCE / AS_CONFED_SET, RFC 5065) are
// accounted for exactly as the standards require: they never count toward the
// AS_PATH length and are skipped when deriving the neighbouring AS.
package bgp

// SegmentType identifies one AS_PATH segment per RFC 4271 section 5.1.2 and
// RFC 5065 section 3.
type SegmentType uint8

const (
	SegASSequence     SegmentType = 1 // AS_SEQUENCE: ordered, counts 1 per AS
	SegASSet          SegmentType = 2 // AS_SET: unordered, counts as length 1
	SegConfedSequence SegmentType = 3 // AS_CONFED_SEQUENCE: counts 0
	SegConfedSet      SegmentType = 4 // AS_CONFED_SET: counts 0
)

// Segment is one AS_PATH segment. AS values are 32-bit unsigned AS numbers.
type Segment struct {
	Type SegmentType
	ASes []uint32
}

// IsConfederation reports whether the segment is internal to a BGP
// confederation and therefore excluded from AS_PATH length accounting.
func (s Segment) IsConfederation() bool {
	return s.Type == SegConfedSequence || s.Type == SegConfedSet
}

// Origin is the ORIGIN attribute; lower values are preferred.
type Origin uint8

const (
	OriginIGP        Origin = 0
	OriginEGP        Origin = 1
	OriginIncomplete Origin = 2
)

// PeerType describes how the local router learned a path. Lower rank values
// win the path-type comparison step.
type PeerType uint8

const (
	PeerEBGP       PeerType = 0 // eBGP peer (different AS, outside the confederation)
	PeerConfedEBGP PeerType = 1 // confederation eBGP peer (different member AS)
	PeerIBGP       PeerType = 2 // iBGP peer (same member AS)
)

// rank implements "eBGP > confed-eBGP > iBGP".
func (p PeerType) rank() int {
	switch p {
	case PeerEBGP:
		return 0
	case PeerConfedEBGP:
		return 1
	default:
		return 2
	}
}

// MissingMEDPolicy selects what value an absent MED attribute is treated as.
type MissingMEDPolicy uint8

const (
	// MissingMEDAsZero treats a missing MED as 0 (the common vendor default).
	MissingMEDAsZero MissingMEDPolicy = 0
	// MissingMEDAsInfinity treats a missing MED as the largest possible value,
	// i.e. the least preferred, matching "bgp bestpath med missing-as-worst".
	MissingMEDAsInfinity MissingMEDPolicy = 1
)

// InfiniteMED is the sentinel used when MissingMEDAsInfinity is configured.
const InfiniteMED uint32 = 0xFFFFFFFF

// NextHopMode controls next-hop rewriting on outbound eBGP advertisements.
type NextHopMode uint8

const (
	// NextHopSelfEBGP rewrites the next hop to the local address on eBGP
	// advertisements (the conventional default).
	NextHopSelfEBGP NextHopMode = 0
	// NextHopPreserve keeps the received next-hop untouched.
	NextHopPreserve NextHopMode = 1
)

// Config is the local router configuration consumed by the selector.
type Config struct {
	// LocalAS is the externally visible autonomous system number carried in
	// AS_SEQUENCE / AS_SET.
	LocalAS uint32
	// MemberAS is the local confederation member AS carried in
	// AS_CONFED_* segments. Zero means the router is not a confederation
	// member and confederation loop filtering is skipped.
	MemberAS uint32
	// RouterID is the local BGP identifier in numeric (host-order) form.
	RouterID uint32
	// ClusterID is the local RR cluster identifier. Zero means this router
	// is not acting as a route reflector.
	ClusterID uint32
	// DeterministicMED groups candidates by neighbouring AS and fully orders
	// each group before cross-group comparison, removing arrival-order
	// dependence. It should normally be enabled.
	DeterministicMED bool
	// AlwaysCompareMED permits MED comparison between paths learned from
	// different neighbouring autonomous systems.
	AlwaysCompareMED bool
	// MissingMED selects the value assigned to paths that omit MED.
	MissingMED MissingMEDPolicy
	// LocalAddr is the address used when NextHopSelfEBGP applies. If nil the
	// next hop is represented by the zero address.
	LocalAddr string
	// NextHopMode selects next-hop rewriting behaviour for eBGP sends.
	NextHopMode NextHopMode
}

// Path is one candidate route for the prefix under evaluation.
type Path struct {
	// ID is an optional caller-assigned stable identifier used in diagnostics
	// and as the final deterministic tie breaker.
	ID        string
	LocalPref uint32
	ASPath    []Segment
	Origin    Origin
	// MEDPresent reports whether MED was received; when false the configured
	// MissingMED policy supplies the comparison value.
	MEDPresent bool
	MED        uint32
	// NextHopReachable mirrors the RIB/FIB reachability check for NextHop.
	NextHopReachable bool
	NextHop          string
	// IGPCost is the interior cost to NextHop; lower is better.
	IGPCost     uint32
	LearnedFrom PeerType
	// OriginatorID is zero when the attribute is absent.
	OriginatorID uint32
	ClusterList  []uint32
	PeerRouterID uint32
	PeerIP       string
	// SourcePeerID optionally identifies the advertising peer so reflection
	// can suppress sending a route back to its originator.
	SourcePeerID string
	// SourceRole classifies the iBGP sender as an RR client or non-client.
	// It is only consulted for iBGP-learned paths during reflection; the zero
	// value RoleIBGPNonClient applies the conservative split-horizon default.
	SourceRole PeerRole
}

// RejectReason explains why a candidate failed legality filtering.
type RejectReason uint8

const (
	RejectNone               RejectReason = 0
	RejectNextHopUnreachable RejectReason = 1
	RejectASLoop             RejectReason = 2
	RejectConfedASLoop       RejectReason = 3
	RejectOriginatorLoop     RejectReason = 4
	RejectClusterLoop        RejectReason = 5
)

// String returns a stable human-readable description.
func (r RejectReason) String() string {
	switch r {
	case RejectNextHopUnreachable:
		return "next-hop unreachable"
	case RejectASLoop:
		return "AS_PATH contains local AS"
	case RejectConfedASLoop:
		return "AS_CONFED path contains local member AS"
	case RejectOriginatorLoop:
		return "ORIGINATOR_ID equals local RouterID"
	case RejectClusterLoop:
		return "CLUSTER_LIST contains local ClusterID"
	default:
		return "accepted"
	}
}

// FilteredPath pairs a rejected candidate with its reason.
type FilteredPath struct {
	Path   *Path
	Reason RejectReason
}

// Result is the output of a best-path evaluation.
type Result struct {
	// Best is the winning path, or nil when no candidate survived filtering.
	Best *Path
	// Rejected holds every discarded candidate with its reason, in input
	// order.
	Rejected []FilteredPath
	// GroupWinners maps each neighbouring-AS group key to its winner. Group
	// key 0 denotes paths with no non-confederation AS (e.g. pure iBGP).
	GroupWinners map[uint32]*Path
}
