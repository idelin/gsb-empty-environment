package bgp

import "testing"

func rrSelector(t *testing.T) *Selector {
	return NewSelector(Config{
		LocalAS:     65000,
		MemberAS:    64512,
		RouterID:    rid(t, "10.0.0.254"),
		ClusterID:   rid(t, "10.0.0.200"),
		LocalAddr:   "10.9.9.9",
		NextHopMode: NextHopSelfEBGP,
	})
}

func samplePath(t *testing.T, learned PeerType) *Path {
	return &Path{
		ID: "best", LocalPref: 100,
		ASPath: []Segment{
			{Type: SegConfedSequence, ASes: []uint32{64500}},
			{Type: SegASSequence, ASes: []uint32{65001}},
		},
		Origin: OriginIGP, MEDPresent: true, MED: 10,
		LearnedFrom: learned, NextHopReachable: true, NextHop: "10.1.1.1",
		PeerIP: "192.0.2.1", PeerRouterID: rid(t, "1.1.1.1"),
		SourcePeerID: "src-peer",
	}
}

func findEntry(rr *ReflectionResult, peer string) ReflectionEntry {
	for _, e := range rr.Entries {
		if e.Target.PeerID == peer {
			return e
		}
	}
	return ReflectionEntry{}
}

// TestReflectionMatrixFromNonClient validates the headline RFC 4456 rule: an
// iBGP route learned from a non-client is reflected to clients but never to
// another non-client.
func TestReflectionMatrixFromNonClient(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerIBGP)

	targets := []Target{
		{PeerID: "client-a", Role: RoleIBGPClient},
		{PeerID: "nonclient-b", Role: RoleIBGPNonClient},
		{PeerID: "ebgp-c", Role: RoleEBGP},
		{PeerID: "src-peer", Role: RoleIBGPNonClient},
	}
	rr := sel.Reflect(best, targets)

	client := findEntry(rr, "client-a")
	if client.Advert == nil {
		t.Fatal("client should receive reflected non-client route")
	}
	// CLUSTER_LIST prepended with local cluster; ORIGINATOR_ID defaulted to RID.
	if got := client.Advert.Path.ClusterList; len(got) != 1 || got[0] != sel.cfg.ClusterID {
		t.Fatalf("client CLUSTER_LIST = %v, want [%v]", got, sel.cfg.ClusterID)
	}
	if client.Advert.Path.OriginatorID != sel.cfg.RouterID {
		t.Fatalf("ORIGINATOR_ID = %v, want local RouterID", client.Advert.Path.OriginatorID)
	}

	nonClient := findEntry(rr, "nonclient-b")
	if nonClient.Advert != nil || nonClient.Reason != SuppressNonClientToNonClient {
		t.Fatalf("non-client reflection: advert=%v reason=%v", nonClient.Advert, nonClient.Reason)
	}

	source := findEntry(rr, "src-peer")
	if source.Advert != nil || source.Reason != SuppressSourcePeer {
		t.Fatalf("source peer: advert=%v reason=%v", source.Advert, source.Reason)
	}

	ebgp := findEntry(rr, "ebgp-c")
	if ebgp.Advert == nil {
		t.Fatal("eBGP peer should receive the route")
	}
}

// TestReflectionFromClient confirms client routes reflect everywhere inside
// the AS, including to non-client peers.
func TestReflectionFromClient(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerIBGP)
	best.SourcePeerID = "client-src"
	best.SourceRole = RoleIBGPClient

	rr := sel.Reflect(best, []Target{
		{PeerID: "client-2", Role: RoleIBGPClient},
		{PeerID: "nonclient-2", Role: RoleIBGPNonClient},
	})
	for _, peer := range []string{"client-2", "nonclient-2"} {
		e := findEntry(rr, peer)
		if e.Advert == nil {
			t.Fatalf("%s should receive client-learned route (reason %v)", peer, e.Reason)
		}
	}
}

// TestReflectionPreservesOriginatorAndChain verifies an existing ORIGINATOR_ID
// is preserved while the local cluster is prepended before prior entries.
func TestReflectionPreservesOriginatorAndChain(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerIBGP)
	origin := rid(t, "5.5.5.5")
	best.OriginatorID = origin
	best.ClusterList = []uint32{rid(t, "7.7.7.7")}

	rr := sel.Reflect(best, []Target{{PeerID: "c", Role: RoleIBGPClient}})
	adv := rr.Entries[0].Advert.Path
	if adv.OriginatorID != origin {
		t.Fatalf("ORIGINATOR_ID mutated: %v", adv.OriginatorID)
	}
	want := []uint32{sel.cfg.ClusterID, rid(t, "7.7.7.7")}
	if len(adv.ClusterList) != 2 || adv.ClusterList[0] != want[0] || adv.ClusterList[1] != want[1] {
		t.Fatalf("CLUSTER_LIST = %v, want %v", adv.ClusterList, want)
	}
	// Input path must not be mutated.
	if len(best.ClusterList) != 1 || best.OriginatorID != origin {
		t.Fatal("Reflect mutated the input path")
	}
}

// TestNonRRSuppressesIBGP confirms an ordinary iBGP speaker never forwards an
// iBGP-learned route to another iBGP peer.
func TestNonRRSuppressesIBGP(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
	})
	best := samplePath(t, PeerIBGP)
	rr := sel.Reflect(best, []Target{
		{PeerID: "a", Role: RoleIBGPClient},
		{PeerID: "b", Role: RoleIBGPNonClient},
	})
	for _, e := range rr.Entries {
		if e.Advert != nil || e.Reason != SuppressIBGPToIBGP {
			t.Fatalf("non-RR iBGP send: advert=%v reason=%v", e.Advert, e.Reason)
		}
	}
}

// TestEBGPOutboundRewrite checks RR attribute stripping, confederation hiding
// and LocalAS prepend on a true eBGP send.
func TestEBGPOutboundRewrite(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerEBGP)
	best.OriginatorID = rid(t, "5.5.5.5")
	best.ClusterList = []uint32{rid(t, "7.7.7.7")}

	rr := sel.Reflect(best, []Target{{PeerID: "ext", Role: RoleEBGP}})
	adv := rr.Entries[0].Advert.Path

	if adv.OriginatorID != 0 || adv.ClusterList != nil {
		t.Fatal("RR attributes must be stripped on eBGP send")
	}
	if adv.NextHop != "10.9.9.9" {
		t.Fatalf("next-hop self = %q, want 10.9.9.9", adv.NextHop)
	}
	if len(adv.ASPath) != 2 {
		t.Fatalf("AS_PATH segments = %d, want 2 (prepend + external)", len(adv.ASPath))
	}
	first := adv.ASPath[0]
	if first.Type != SegASSequence || len(first.ASes) != 1 || first.ASes[0] != 65000 {
		t.Fatalf("prepend = %+v, want AS_SEQUENCE [65000]", first)
	}
	for _, seg := range adv.ASPath {
		if seg.IsConfederation() {
			t.Fatal("confederation segment leaked to external peer")
		}
	}
}

// TestConfedEBGPOutbound verifies a confederation-eBGP send keeps confed
// segments and prepends the member AS with confederation encoding.
func TestConfedEBGPOutbound(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerConfedEBGP)

	rr := sel.Reflect(best, []Target{{
		PeerID: "member", Role: RoleEBGP, Confederation: true,
	}})
	adv := rr.Entries[0].Advert.Path

	first := adv.ASPath[0]
	if first.Type != SegConfedSequence || first.ASes[0] != 64512 {
		t.Fatalf("confed prepend = %+v, want AS_CONFED_SEQUENCE [64512]", first)
	}
	found := false
	for _, seg := range adv.ASPath {
		if seg.IsConfederation() {
			found = true
		}
	}
	if !found {
		t.Fatal("confederation segments must be preserved inside confederation")
	}
}

// TestEBGPLearnedReflectedToAllIBGP ensures externally learned routes are
// advertised (with RR attributes when reflected) to every iBGP peer.
func TestEBGPLearnedReflectedToAllIBGP(t *testing.T) {
	sel := rrSelector(t)
	best := samplePath(t, PeerEBGP)
	rr := sel.Reflect(best, []Target{
		{PeerID: "c", Role: RoleIBGPClient},
		{PeerID: "nc", Role: RoleIBGPNonClient},
	})
	for _, e := range rr.Entries {
		if e.Advert == nil {
			t.Fatalf("eBGP route must reach iBGP peer %s: %v", e.Target.PeerID, e.Reason)
		}
		if e.Advert.Path.ClusterList[0] != sel.cfg.ClusterID {
			t.Fatalf("peer %s missing local cluster prepend", e.Target.PeerID)
		}
	}

	// Same route on a non-RR still reaches iBGP peers but carries no RR attrs.
	plain := NewSelector(Config{LocalAS: 65000, RouterID: rid(t, "10.0.0.254")})
	out := plain.Reflect(samplePath(t, PeerEBGP), []Target{{PeerID: "x", Role: RoleIBGPClient}})
	adv := out.Entries[0].Advert
	if adv == nil || adv.AttributesAdded || adv.Path.OriginatorID != 0 || adv.Path.ClusterList != nil {
		t.Fatalf("non-RR external reflection = %+v, want no RR attributes", adv)
	}
}
