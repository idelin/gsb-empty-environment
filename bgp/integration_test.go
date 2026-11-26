package bgp

import "testing"

// TestConfederationPathTypePreference verifies confed-eBGP beats iBGP while
// eBGP beats confed-eBGP, and that confederation segments stay length zero.
func TestConfederationPathTypePreference(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, MemberAS: 64512,
		RouterID: rid(t, "10.0.0.254"), DeterministicMED: true,
	})
	mk := func(id string, learned PeerType) *Path {
		return &Path{
			ID: id, LocalPref: 100,
			ASPath: []Segment{
				{Type: SegConfedSequence, ASes: []uint32{64500}},
				{Type: SegASSequence, ASes: []uint32{65001}},
			},
			Origin: OriginIGP, MEDPresent: true, MED: 10,
			LearnedFrom: learned, NextHopReachable: true,
			IGPCost: 100, PeerIP: "192.0.2." + id,
			PeerRouterID: rid(t, "8.8.8.8"),
		}
	}

	res := sel.Select([]*Path{mk("1", PeerIBGP), mk("2", PeerConfedEBGP)})
	if res.Best.ID != "2" {
		t.Fatalf("confed-eBGP vs iBGP: best = %s, want 2", res.Best.ID)
	}

	res = sel.Select([]*Path{mk("1", PeerConfedEBGP), mk("2", PeerEBGP)})
	if res.Best.ID != "2" {
		t.Fatalf("eBGP vs confed-eBGP: best = %s, want 2", res.Best.ID)
	}

	// Both candidates share external length 1 despite the confed segment.
	if l := ASPathLength(res.Best.ASPath); l != 1 {
		t.Fatalf("confederation AS_PATH length = %d, want 1", l)
	}
}

// TestEndToEndSelectAndReflect wires filtering, deterministic selection and
// the reflection matrix into one workflow.
func TestEndToEndSelectAndReflect(t *testing.T) {
	sel := rrSelector(t)

	validClient := &Path{
		ID: "from-client", LocalPref: 100,
		ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
		Origin: OriginIGP, MEDPresent: true, MED: 20,
		LearnedFrom: PeerIBGP, SourceRole: RoleIBGPClient,
		NextHopReachable: true, IGPCost: 10,
		PeerIP: "192.0.2.10", PeerRouterID: rid(t, "1.1.1.1"),
		SourcePeerID: "client-1",
	}
	looped := &Path{
		ID: "looped", NextHopReachable: true,
		OriginatorID: sel.cfg.RouterID,
		ASPath:       []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
	}

	res := sel.Select([]*Path{looped, validClient})
	if res.Best == nil || res.Best.ID != "from-client" {
		t.Fatalf("selection = %+v", res.Best)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Reason != RejectOriginatorLoop {
		t.Fatalf("rejected = %+v", res.Rejected)
	}

	rr := sel.Reflect(res.Best, []Target{
		{PeerID: "client-1", Role: RoleIBGPClient},
		{PeerID: "client-2", Role: RoleIBGPClient},
		{PeerID: "nonclient-1", Role: RoleIBGPNonClient},
		{PeerID: "external-1", Role: RoleEBGP},
	})

	decisions := map[string]bool{}
	for _, e := range rr.Entries {
		decisions[e.Target.PeerID] = e.Advert != nil
	}
	want := map[string]bool{
		"client-1": false, // source peer suppression
		"client-2": true,
		// client-learned route may reflect to non-clients
		"nonclient-1": true,
		"external-1":  true,
	}
	for peer, advertised := range want {
		if decisions[peer] != advertised {
			t.Fatalf("peer %s advertised=%v, want %v", peer, decisions[peer], advertised)
		}
	}
}

func TestRejectReasonStrings(t *testing.T) {
	for r, want := range map[RejectReason]string{
		RejectNone:               "accepted",
		RejectNextHopUnreachable: "next-hop unreachable",
		RejectASLoop:             "AS_PATH contains local AS",
		RejectConfedASLoop:       "AS_CONFED path contains local member AS",
		RejectOriginatorLoop:     "ORIGINATOR_ID equals local RouterID",
		RejectClusterLoop:        "CLUSTER_LIST contains local ClusterID",
	} {
		if got := r.String(); got != want {
			t.Fatalf("RejectReason(%d) = %q, want %q", r, got, want)
		}
	}
}

func TestSuppressReasonStrings(t *testing.T) {
	for r, want := range map[SuppressReason]string{
		SuppressNone:                 "advertised",
		SuppressSourcePeer:           "route originator",
		SuppressIBGPToIBGP:           "iBGP route on non route-reflector",
		SuppressNonClientToNonClient: "non-client iBGP route not reflected to non-client",
	} {
		if got := r.String(); got != want {
			t.Fatalf("SuppressReason(%d) = %q, want %q", r, got, want)
		}
	}
}

func TestNextHopPreserveMode(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
		NextHopMode: NextHopPreserve,
	})
	best := samplePath(t, PeerEBGP)
	best.NextHop = "172.16.0.7"
	rr := sel.Reflect(best, []Target{{PeerID: "ext", Role: RoleEBGP}})
	if got := rr.Entries[0].Advert.Path.NextHop; got != "172.16.0.7" {
		t.Fatalf("preserve mode next hop = %q, want unchanged 172.16.0.7", got)
	}
}
