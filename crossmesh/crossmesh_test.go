package crossmesh

import "testing"

func dom(v string) Name { return Name{Kind: KindDomain, Value: v} }
func uri(v string) Name { return Name{Kind: KindURI, Value: v} }

func edge(id, from, to string, includes, excludes []Name, actions []string,
	depth int, nbf, naf int64) DelegationEdge {
	return DelegationEdge{
		ID:               id,
		Issuer:           from,
		Subject:          to,
		AllowedPrefixes:  includes,
		ExcludedSubtrees: excludes,
		Actions:          actions,
		MaxPathLen:       depth,
		NotBefore:        nbf,
		NotAfter:         naf,
	}
}

func mustBuild(t *testing.T, g Graph) *Engine {
	t.Helper()
	eng, err := New(g)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return eng
}

func assertAuth(t *testing.T, eng *Engine, node string, name Name, action string,
	at int64, want bool) {
	t.Helper()
	got, err := eng.IsAuthorized(node, name, action, at)
	if err != nil {
		t.Fatalf("IsAuthorized(%s): %v", node, err)
	}
	if got != want {
		t.Fatalf("IsAuthorized node=%s name=%q action=%q at=%d = %v, want %v",
			node, name.Value, action, at, got, want)
	}
}

const (
	t0 int64 = 0
	t1 int64 = 10
	t2 int64 = 20
	t3 int64 = 30
)

// A hop budget of zero authorizes the subject itself but never one more
// delegation hop; a larger budget unlocks the deeper node.
func TestDepthExhaustion(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "mid", "leaf", "deep"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("e1", "root", "mid", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 0, t0, t3),
			edge("e2", "mid", "leaf", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 0, t0, t3),
			edge("e3", "root", "mid", []Name{dom("example.org")}, nil,
				[]string{"sign"}, 2, t0, t3),
			edge("e4", "mid", "deep", []Name{dom("example.org")}, nil,
				[]string{"sign"}, 1, t0, t3),
		},
	}
	eng := mustBuild(t, g)

	assertAuth(t, eng, "mid", dom("a.example.com"), "sign", t1, true)
	assertAuth(t, eng, "leaf", dom("a.example.com"), "sign", t1, false)
	assertAuth(t, eng, "deep", dom("example.org"), "sign", t1, true)

	vs, _ := eng.AuthorizedVoxels("leaf")
	if !vs.isEmpty() {
		t.Fatalf("leaf should hold no voxels, got %d", len(vs.m))
	}
}

// Authority existing only on a rootless cycle never becomes trusted; removing
// the sole root edge isolates the whole cycle.
func TestCycleIsolation(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "a", "b", "c"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("root-a", "root", "a", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 5, t0, t3),
			edge("a-b", "a", "b", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 5, t0, t3),
			edge("b-c", "b", "c", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 5, t0, t3),
			edge("c-a", "c", "a", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 5, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	assertAuth(t, eng, "c", dom("x.example.com"), "sign", t1, true)

	report, err := eng.Revoke([]Revocation{
		{EdgeID: "root-a", ExpectedDigest: eng.Digest()},
	})
	if err != nil || !report.Applied {
		t.Fatalf("revoke failed: applied=%v err=%v", report.Applied, err)
	}
	for _, n := range []string{"a", "b", "c"} {
		vs, _ := eng.AuthorizedVoxels(n)
		if !vs.isEmpty() {
			t.Fatalf("rootless cycle node %s retained authority: %d voxels", n, len(vs.m))
		}
	}
	if len(report.LostSupport) != 3 {
		t.Fatalf("LostSupport = %v, want all three cycle nodes", report.LostSupport)
	}
}

// An excluded subtree tears scope along the whole chain: names inside the
// exclusion can never be re-authorized by a later include prefix.
func TestExclusionTearing(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "ca", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("root-ca", "root", "ca", []Name{dom("example.com")},
				[]Name{dom("secret.example.com")}, []string{"sign"}, 3, t0, t3),
			edge("ca-leaf", "ca", "leaf", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 2, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	assertAuth(t, eng, "leaf", dom("www.example.com"), "sign", t1, true)
	assertAuth(t, eng, "leaf", dom("secret.example.com"), "sign", t1, false)
	assertAuth(t, eng, "leaf", dom("deep.secret.example.com"), "sign", t1, false)

	slices, err := eng.Slices("leaf")
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	for _, s := range slices {
		for _, c := range s.Cells {
			if c.Prefix.Value == "secret.example.com" {
				t.Fatalf("excluded subtree leaked into retained slice: %+v", s)
			}
		}
	}
	if len(slices) == 0 {
		t.Fatal("leaf should retain at least the non-excluded scope slice")
	}
}

// Two parent chains authorize different (time, action) halves of one scope. A
// per-dimension union would fabricate (early, write) and (late, read) combos
// that no single complete chain ever granted.
func TestDiamondNoCrossDimensionStitching(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "p1", "p2", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-p1", "root", "p1", []Name{dom("example.com")}, nil,
				[]string{"read"}, 2, t0, t2),
			edge("r-p2", "root", "p2", []Name{dom("example.com")}, nil,
				[]string{"write"}, 2, t1, t3),
			edge("p1-l", "p1", "leaf", []Name{dom("example.com")}, nil,
				[]string{"read"}, 1, t0, t2),
			edge("p2-l", "p2", "leaf", []Name{dom("example.com")}, nil,
				[]string{"write"}, 1, t1, t3),
		},
	}
	eng := mustBuild(t, g)

	assertAuth(t, eng, "leaf", dom("example.com"), "read", t1, true)
	assertAuth(t, eng, "leaf", dom("example.com"), "write", t2, true)
	assertAuth(t, eng, "leaf", dom("example.com"), "write", t0, false)
	assertAuth(t, eng, "leaf", dom("example.com"), "read", t2, false)
	assertAuth(t, eng, "leaf", dom("example.com"), "read", t2-1, true)

	slices, _ := eng.Slices("leaf")
	for _, s := range slices {
		if len(s.Actions) == 2 {
			t.Fatalf("slice merged actions from distinct chains: %+v", s)
		}
	}
}

// Revoking one relay preserves slices backed by an independent diamond branch
// while removing slices only the broken branch supported.
func TestRevocationPrunesOnlyUnsupportedSlices(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "a", "b", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-a", "root", "a", []Name{dom("a.example.com")}, nil,
				[]string{"read"}, 2, t0, t3),
			edge("r-b", "root", "b", []Name{dom("b.example.com")}, nil,
				[]string{"write"}, 2, t0, t3),
			edge("a-l", "a", "leaf", []Name{dom("a.example.com")}, nil,
				[]string{"read"}, 1, t0, t3),
			edge("b-l", "b", "leaf", []Name{dom("b.example.com")}, nil,
				[]string{"write"}, 1, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	beforeDigest := eng.Digest()

	report, err := eng.Revoke([]Revocation{
		{EdgeID: "r-a", ExpectedDigest: beforeDigest},
	})
	if err != nil || !report.Applied {
		t.Fatalf("revoke: applied=%v err=%v", report.Applied, err)
	}
	assertAuth(t, eng, "leaf", dom("a.example.com"), "read", t1, false)
	assertAuth(t, eng, "leaf", dom("b.example.com"), "write", t1, true)

	var leafReport *NodeReport
	for i := range report.Reports {
		if report.Reports[i].Node == "leaf" {
			leafReport = &report.Reports[i]
		}
	}
	if leafReport == nil {
		t.Fatal("leaf missing from revocation report")
	}
	if !leafReport.StillHasAuthority {
		t.Fatal("leaf should retain the independent b-branch slice")
	}
	if len(leafReport.Retained) == 0 || len(leafReport.Lost) == 0 {
		t.Fatalf("want both retained and lost slices, got %+v", leafReport)
	}
}

// A stale digest fails closed: no edge is removed and the digest is stable.
func TestCASConflictFailsClosed(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-l", "root", "leaf", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 1, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	digest := eng.Digest()

	report, err := eng.Revoke([]Revocation{
		{EdgeID: "r-l", ExpectedDigest: "deadbeef"},
	})
	if err != nil {
		t.Fatalf("conflict should be reported, not error: %v", err)
	}
	if report.Applied || report.ConflictEdgeID != "r-l" {
		t.Fatalf("CAS conflict not detected: %+v", report)
	}
	if eng.Digest() != digest {
		t.Fatal("digest changed despite failed CAS")
	}
	assertAuth(t, eng, "leaf", dom("example.com"), "sign", t1, true)

	if _, err := eng.Revoke([]Revocation{
		{EdgeID: "missing", ExpectedDigest: digest},
	}); err == nil {
		t.Fatal("unknown edge revocation must error")
	}
}

// The independent exhaustive acceptor must agree with the fixed-point engine
// on every node of a small graph containing depth limits, a cycle, a diamond
// and an exclusion tear.
func TestIndependentAcceptorAgrees(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "p1", "p2", "x", "y", "z"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-p1", "root", "p1", []Name{dom("example.com")},
				[]Name{dom("blocked.example.com")}, []string{"read", "write"}, 3, t0, t3),
			edge("r-p2", "root", "p2", []Name{dom("example.com")}, nil,
				[]string{"read"}, 3, t1, t3),
			edge("p1-x", "p1", "x", []Name{dom("example.com")}, nil,
				[]string{"read"}, 2, t0, t2),
			edge("p2-x", "p2", "x", []Name{dom("example.com")}, nil,
				[]string{"read"}, 2, t0, t3),
			edge("x-y", "x", "y", []Name{dom("example.com")}, nil,
				[]string{"read"}, 1, t0, t3),
			edge("y-z", "y", "z", []Name{dom("example.com")}, nil,
				[]string{"read"}, 0, t0, t3),
			edge("z-x", "z", "x", []Name{dom("example.com")}, nil,
				[]string{"read"}, 0, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	acc, err := NewIndependentAcceptor(eng)
	if err != nil {
		t.Fatalf("acceptor construction: %v", err)
	}
	for _, n := range g.Nodes {
		ok, err := acc.Accept(n)
		if err != nil {
			t.Fatalf("Accept(%s): %v", n, err)
		}
		if !ok {
			t.Fatalf("independent acceptor disagrees with engine on node %s", n)
		}
	}

	// The acceptor must refuse large graphs rather than enumerate exponentially.
	big := make([]string, IndependentAcceptorMaxNodes+1)
	for i := range big {
		big[i] = "n" + itoa(i)
	}
	if _, err := NewIndependentAcceptor(mustBuild(t, Graph{
		Nodes:        big,
		TrustedRoots: []string{big[0]},
	})); err == nil {
		t.Fatal("acceptor must reject graphs above its node cap")
	}
}

// URI prefixes use literal component boundaries; sibling prefixes must not
// shadow each other, and output slices must be pairwise disjoint.
func TestURIPrefixBoundariesAndDisjointSlices(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-l", "root", "leaf",
				[]Name{uri("https://api.example.com/v1")},
				[]Name{uri("https://api.example.com/v1/admin")},
				[]string{"call"}, 1, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	assertAuth(t, eng, "leaf", uri("https://api.example.com/v1/users"), "call", t1, true)
	assertAuth(t, eng, "leaf", uri("https://api.example.com/v1/admin/x"), "call", t1, false)
	assertAuth(t, eng, "leaf", uri("https://api.example.com/v10"), "call", t1, false)

	slices, err := eng.Slices("leaf")
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	if !slicesAreDisjoint(slices) {
		t.Fatalf("rendered slices overlap: %+v", slices)
	}
}

func slicesAreDisjoint(slices []Slice) bool {
	type voxel struct {
		cell    string
		window  TimeWindow
		actions string
	}
	seen := map[voxel]struct{}{}
	for _, s := range slices {
		key := voxel{window: s.Window, actions: joinActions(s.Actions)}
		for _, c := range s.Cells {
			key.cell = c.Display
			if _, dup := seen[key]; dup {
				return false
			}
			seen[key] = struct{}{}
		}
	}
	return true
}

func joinActions(actions []string) string {
	out := ""
	for _, a := range actions {
		out += a + "|"
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// A zero-limit edge delivers terminal authority but must never be extended,
// even when the same node is also reachable through a deeper independent
// branch. This locks the per-state budget separation against regressions.
func TestZeroLimitEdgeNeverExtends(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "mid", "term", "deep"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			// Terminal branch: mid may issue, but the edge into term allows no
			// further delegation.
			edge("r-mid", "root", "mid", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 1, t0, t3),
			edge("mid-term", "mid", "term", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 0, t0, t3),
			edge("term-deep", "term", "deep", []Name{dom("example.com")}, nil,
				[]string{"sign"}, 0, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	assertAuth(t, eng, "term", dom("example.com"), "sign", t1, true)
	assertAuth(t, eng, "deep", dom("example.com"), "sign", t1, false)

	termSlices, err := eng.Slices("term")
	if err != nil || len(termSlices) == 0 {
		t.Fatalf("term should hold terminal slices: %v", err)
	}
	if !slicesAreDisjoint(termSlices) {
		t.Fatal("term slices are not disjoint")
	}
}

// Rendered retained slices across a diamond must remain pairwise disjoint in
// the full (cell, time window, action) product.
func TestRenderedSlicesAreDisjointAcrossDiamond(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "p1", "p2", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-p1", "root", "p1", []Name{dom("example.com"), dom("a.example.com")},
				nil, []string{"read", "write"}, 2, t0, t3),
			edge("r-p2", "root", "p2", []Name{dom("example.com"), dom("b.example.com")},
				nil, []string{"read", "admin"}, 2, t1, t3),
			edge("p1-l", "p1", "leaf", []Name{dom("example.com"), dom("a.example.com")},
				nil, []string{"read", "write"}, 1, t0, t3),
			edge("p2-l", "p2", "leaf", []Name{dom("example.com"), dom("b.example.com")},
				nil, []string{"read", "admin"}, 1, t1, t3),
		},
	}
	eng := mustBuild(t, g)
	slices, err := eng.Slices("leaf")
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	if !slicesAreDisjoint(slices) {
		t.Fatalf("diamond slices overlap: %+v", slices)
	}
}

// When two chains grant different actions in the exact same scope cell and
// time interval, rendering must keep them as separate single-action slices.
// Emitting a combined-action slice would assert an authorization no single
// complete chain ever provided.
func TestSameColumnDifferentChainActionsStaySeparate(t *testing.T) {
	g := Graph{
		Nodes:        []string{"root", "p1", "p2", "leaf"},
		TrustedRoots: []string{"root"},
		Edges: []DelegationEdge{
			edge("r-p1", "root", "p1", []Name{dom("example.com")}, nil,
				[]string{"read"}, 2, t0, t3),
			edge("r-p2", "root", "p2", []Name{dom("example.com")}, nil,
				[]string{"write"}, 2, t0, t3),
			edge("p1-l", "p1", "leaf", []Name{dom("example.com")}, nil,
				[]string{"read"}, 1, t0, t3),
			edge("p2-l", "p2", "leaf", []Name{dom("example.com")}, nil,
				[]string{"write"}, 1, t0, t3),
		},
	}
	eng := mustBuild(t, g)
	slices, err := eng.Slices("leaf")
	if err != nil {
		t.Fatalf("Slices: %v", err)
	}
	for _, s := range slices {
		if len(s.Actions) != 1 {
			t.Fatalf("slice must carry exactly one action, got %+v", s)
		}
	}
	assertAuth(t, eng, "leaf", dom("example.com"), "read", t1, true)
	assertAuth(t, eng, "leaf", dom("example.com"), "write", t1, true)
}
