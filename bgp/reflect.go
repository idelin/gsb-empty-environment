package bgp

// PeerRole classifies a target peer relative to the local router.
type PeerRole uint8

const (
	RoleIBGPNonClient PeerRole = 0 // iBGP non-client peer (conservative zero value)
	RoleIBGPClient    PeerRole = 1 // iBGP route-reflector client
	RoleEBGP          PeerRole = 2 // external or confederation-eBGP peer
)

// SuppressReason explains why an advertisement is withheld from a peer.
type SuppressReason uint8

const (
	SuppressNone                 SuppressReason = 0
	SuppressSourcePeer           SuppressReason = 1 // never advertise back to the source
	SuppressIBGPToIBGP           SuppressReason = 2 // non-RR: iBGP learned routes stay local
	SuppressNonClientToNonClient SuppressReason = 3 // RFC 4456 split-horizon rule
)

// String returns a stable human-readable description.
func (r SuppressReason) String() string {
	switch r {
	case SuppressSourcePeer:
		return "route originator"
	case SuppressIBGPToIBGP:
		return "iBGP route on non route-reflector"
	case SuppressNonClientToNonClient:
		return "non-client iBGP route not reflected to non-client"
	default:
		return "advertised"
	}
}

// Target describes one outbound peer.
type Target struct {
	// PeerID is matched against Path.SourcePeerID to suppress reflection back
	// to the route originator.
	PeerID string
	Role   PeerRole
	// Address is informational; the advertisement carries the next hop rather
	// than the peer address.
	Address string
	// Confederation marks an RoleEBGP peer as a confederation-eBGP peer in
	// another member AS. Such peers receive AS_CONFED_SEQUENCE prepends and
	// retain the path's confederation segments; true eBGP peers see them
	// stripped and the external LocalAS prepended.
	Confederation bool
}

// Advertisement is the per-peer outbound route derived from a best path.
type Advertisement struct {
	// Path is an independent, rewritten copy of the winning path.
	Path *Path
	// AttributesAdded reports whether RR attributes were attached/updated.
	AttributesAdded bool
}

// ReflectionEntry is the decision for one target peer.
type ReflectionEntry struct {
	Target Target
	// Advert is nil when the route must not be sent; Reason then explains it.
	Advert *Advertisement
	Reason SuppressReason
}

// ReflectionResult holds the full client/non-client/eBGP decision matrix.
type ReflectionResult struct {
	Entries []ReflectionEntry
}

// isRR reports whether the local router acts as a route reflector. Only RRs
// carry a configured ClusterID and may reflect iBGP-learned routes.
func (c *Config) isRR() bool { return c.ClusterID != 0 }

// Reflect builds the advertisement matrix for best toward every target peer.
// best must be a path that has already passed legality filtering. The input
// path is never mutated.
func (s *Selector) Reflect(best *Path, targets []Target) *ReflectionResult {
	out := &ReflectionResult{Entries: make([]ReflectionEntry, 0, len(targets))}
	for _, target := range targets {
		entry := ReflectionEntry{Target: target}
		if best.SourcePeerID != "" && target.PeerID == best.SourcePeerID {
			entry.Reason = SuppressSourcePeer
			out.Entries = append(out.Entries, entry)
			continue
		}

		switch target.Role {
		case RoleEBGP:
			entry.Advert = s.buildEBGPAdvert(best, target.Confederation)
		case RoleIBGPClient, RoleIBGPNonClient:
			entry = s.buildIBGPReflection(best, target)
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}

// buildIBGPReflection applies the RFC 4456 client/non-client matrix and RR
// attribute rewriting.
func (s *Selector) buildIBGPReflection(best *Path, target Target) ReflectionEntry {
	entry := ReflectionEntry{Target: target}

	// Routes learned from eBGP / confed-eBGP are advertised to all iBGP
	// speakers; iBGP-learned routes require route-reflector processing.
	if best.LearnedFrom == PeerIBGP {
		if !s.cfg.isRR() {
			entry.Reason = SuppressIBGPToIBGP
			return entry
		}
		// RFC 4456 section 8: a route from an iBGP non-client peer must not
		// be reflected to another non-client peer; client routes may be.
		if target.Role == RoleIBGPNonClient && best.SourceRole != RoleIBGPClient {
			entry.Reason = SuppressNonClientToNonClient
			return entry
		}
	}

	advert := &Advertisement{Path: clonePath(best)}

	// RR attributes are only attached by an acting route reflector.
	if s.cfg.isRR() {
		if advert.Path.OriginatorID == 0 {
			if best.OriginatorID != 0 {
				advert.Path.OriginatorID = best.OriginatorID
			} else {
				advert.Path.OriginatorID = s.cfg.RouterID
			}
			advert.AttributesAdded = true
		}
		// Prepend the local cluster id; existing CLUSTER_LIST is preserved.
		advert.Path.ClusterList = prependCluster(s.cfg.ClusterID, best.ClusterList)
		advert.AttributesAdded = true
	}

	entry.Advert = advert
	return entry
}

// prependCluster returns a fresh slice with local first, followed by the
// received CLUSTER_LIST, per RFC 4456 section 8.
func prependCluster(local uint32, existing []uint32) []uint32 {
	out := make([]uint32, 0, len(existing)+1)
	out = append(out, local)
	out = append(out, existing...)
	return out
}

// buildEBGPAdvert produces the externally advertised route. RR loop-prevention
// attributes are stripped and the appropriate AS is prepended: LocalAS on a
// true eBGP send (confederation internals hidden), MemberAS as
// AS_CONFED_SEQUENCE on a confederation-eBGP send.
func (s *Selector) buildEBGPAdvert(best *Path, confederation bool) *Advertisement {
	out := clonePath(best)

	out.OriginatorID = 0
	out.ClusterList = nil

	out.ASPath = s.egressASPath(best, confederation)

	if s.cfg.NextHopMode == NextHopSelfEBGP {
		out.NextHop = s.cfg.LocalAddr
	}
	// Re-advertised paths originate from this router from the peer's view.
	out.SourcePeerID = ""
	return &Advertisement{Path: out}
}

// egressASPath rewrites AS_PATH for an outbound eBGP update.
func (s *Selector) egressASPath(best *Path, confederation bool) []Segment {
	if confederation {
		// Within the confederation every segment is preserved; only the local
		// member AS is prepended using the confederation encoding.
		prepended := Segment{Type: SegConfedSequence, ASes: []uint32{s.cfg.MemberAS}}
		return append([]Segment{prepended}, cloneSegments(best.ASPath)...)
	}

	// True eBGP: hide all confederation internals and prepend LocalAS.
	external := make([]Segment, 0, len(best.ASPath)+1)
	external = append(external, Segment{Type: SegASSequence, ASes: []uint32{s.cfg.LocalAS}})
	for _, seg := range best.ASPath {
		if seg.IsConfederation() {
			continue
		}
		external = append(external, cloneSegment(seg))
	}
	return external
}

// clonePath returns a deep enough copy for advertisement rewriting.
func clonePath(p *Path) *Path {
	cp := *p
	cp.ASPath = cloneSegments(p.ASPath)
	if p.ClusterList != nil {
		cp.ClusterList = append([]uint32(nil), p.ClusterList...)
	}
	return &cp
}

func cloneSegments(in []Segment) []Segment {
	out := make([]Segment, len(in))
	for i, seg := range in {
		out[i] = cloneSegment(seg)
	}
	return out
}

func cloneSegment(seg Segment) Segment {
	cp := seg
	if seg.ASes != nil {
		cp.ASes = append([]uint32(nil), seg.ASes...)
	}
	return cp
}
