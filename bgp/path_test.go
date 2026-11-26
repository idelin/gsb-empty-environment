package bgp

import "testing"

// TestASPathLengthAndNeighborAS covers RFC 5065 zero-cost confederation
// segments and first non-confederation neighbour AS extraction.
func TestASPathLengthAndNeighborAS(t *testing.T) {
	path := []Segment{
		{Type: SegConfedSequence, ASes: []uint32{64512, 64513}},
		{Type: SegConfedSet, ASes: []uint32{64514}},
		{Type: SegASSequence, ASes: []uint32{65001, 65002, 65003}},
		{Type: SegASSet, ASes: []uint32{65004, 65005}},
	}
	if got, want := ASPathLength(path), 4; got != want {
		t.Fatalf("ASPathLength = %d, want %d (3 sequence + 1 set, confed zero)", got, want)
	}
	if got, want := NeighborAS(path), uint32(65001); got != want {
		t.Fatalf("NeighborAS = %d, want %d (leading confed segments skipped)", got, want)
	}

	// AS_SET as first external segment: smallest member is the deterministic key.
	setFirst := []Segment{
		{Type: SegConfedSequence, ASes: []uint32{64512}},
		{Type: SegASSet, ASes: []uint32{65020, 65010, 65015}},
	}
	if got, want := NeighborAS(setFirst), uint32(65010); got != want {
		t.Fatalf("NeighborAS(AS_SET) = %d, want %d", got, want)
	}

	// Pure confederation / iBGP path: no external segment -> group key 0.
	internal := []Segment{{Type: SegConfedSequence, ASes: []uint32{64512, 64513}}}
	if got := ASPathLength(internal); got != 0 {
		t.Fatalf("confed-only ASPathLength = %d, want 0", got)
	}
	if got := NeighborAS(internal); got != 0 {
		t.Fatalf("confed-only NeighborAS = %d, want 0", got)
	}

	// Empty AS_SET contributes no length.
	emptySet := []Segment{{Type: SegASSet, ASes: nil}}
	if got := ASPathLength(emptySet); got != 0 {
		t.Fatalf("empty AS_SET length = %d, want 0", got)
	}
}

// TestLegalityFilters exercises every mandatory discard reason.
func TestLegalityFilters(t *testing.T) {
	cfg := Config{
		LocalAS: 65000, MemberAS: 64512,
		RouterID: rid(t, "10.0.0.254"), ClusterID: rid(t, "10.0.0.200"),
		DeterministicMED: true,
	}
	sel := NewSelector(cfg)

	good := &Path{
		ID: "good", LocalPref: 100,
		ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
		Origin: OriginIGP, MEDPresent: true, MED: 10,
		LearnedFrom: PeerEBGP, NextHopReachable: true,
		PeerIP: "192.0.2.1", PeerRouterID: rid(t, "1.1.1.1"),
	}
	nextHopBad := &Path{
		ID: "nh", NextHopReachable: false,
		ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
	}
	asLoop := &Path{
		ID: "asloop", NextHopReachable: true,
		ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65000, 65001}}},
	}
	confedLoop := &Path{
		ID: "confedloop", NextHopReachable: true,
		ASPath: []Segment{
			{Type: SegConfedSequence, ASes: []uint32{64512}},
			{Type: SegASSequence, ASes: []uint32{65001}},
		},
	}
	originatorLoop := &Path{
		ID: "originloop", NextHopReachable: true,
		OriginatorID: cfg.RouterID,
		ASPath:       []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
	}
	clusterLoop := &Path{
		ID: "clusterloop", NextHopReachable: true,
		ClusterList: []uint32{cfg.ClusterID},
		ASPath:      []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
	}

	res := sel.Select([]*Path{good, nextHopBad, asLoop, confedLoop, originatorLoop, clusterLoop})
	if res.Best == nil || res.Best.ID != "good" {
		t.Fatalf("best = %v, want good", res.Best)
	}

	reasons := map[string]RejectReason{}
	for _, fp := range res.Rejected {
		reasons[fp.Path.ID] = fp.Reason
	}
	want := map[string]RejectReason{
		"nh":          RejectNextHopUnreachable,
		"asloop":      RejectASLoop,
		"confedloop":  RejectConfedASLoop,
		"originloop":  RejectOriginatorLoop,
		"clusterloop": RejectClusterLoop,
	}
	for id, reason := range want {
		if reasons[id] != reason {
			t.Fatalf("path %s: got %v, want %v", id, reasons[id], reason)
		}
	}

	// All-rejected table yields no best path.
	empty := sel.Select([]*Path{nextHopBad})
	if empty.Best != nil || len(empty.Rejected) != 1 {
		t.Fatalf("all-rejected: best=%v rejected=%d", empty.Best, len(empty.Rejected))
	}
}

func TestRouterIDRoundTrip(t *testing.T) {
	cases := map[string]uint32{
		"0.0.0.0":         0,
		"10.0.0.254":      0x0A0000FE,
		"255.255.255.255": 0xFFFFFFFF,
		"192.168.1.1":     0xC0A80101,
	}
	for s, want := range cases {
		got, err := ParseRouterID(s)
		if err != nil || got != want {
			t.Fatalf("ParseRouterID(%s) = %x, %v; want %x", s, got, err, want)
		}
		if back := FormatRouterID(got); back != s {
			t.Fatalf("FormatRouterID(%x) = %s, want %s", got, back, s)
		}
	}
	if _, err := ParseRouterID("::1"); err == nil {
		t.Fatal("ParseRouterID accepted IPv6 address")
	}
}

func TestCompareIP(t *testing.T) {
	if CompareIP("10.0.0.1", "10.0.0.2") >= 0 {
		t.Fatal("IPv4 numeric ordering failed")
	}
	if CompareIP("10.0.0.1", "10.0.0.1") != 0 {
		t.Fatal("equal IPv4 failed")
	}
	if CompareIP("10.0.0.1", "2001:db8::1") >= 0 {
		t.Fatal("IPv4 must sort before IPv6")
	}
	if CompareIP("not-an-ip", "10.0.0.1") <= 0 {
		t.Fatal("invalid address must sort after valid")
	}
	if CompareIP("2001:db8::2", "2001:db8::1") <= 0 {
		t.Fatal("IPv6 numeric ordering failed")
	}
}
