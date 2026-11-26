package archiveproof

import (
	"errors"
	"testing"
)

// bruteMinimum independently enumerates every subset of certificate
// candidates.  It is intentionally exponential; it exists to validate the
// production search on the small graphs used by the tests.
func bruteMinimum(t *testing.T, a *Archive) (int, bool) {
	t.Helper()
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	idx, err := a.buildIndex()
	if err != nil {
		t.Fatal(err)
	}
	domains, err := a.ClockDomains()
	if err != nil {
		t.Fatal(err)
	}
	cs, domains, err := buildCandidateSet(a, idx, domains)
	if err != nil {
		t.Fatal(err)
	}
	if cs.len() > 20 {
		t.Fatalf("brute force graph too large: %d", cs.len())
	}
	edges := buildEdges(a, idx, cs)
	accept, err := resolveReferenceGroups(cs, idx, a.Acceptance)
	if err != nil {
		return 0, false
	}
	genesisGroups, err := resolveReferenceGroups(cs, idx, a.Genesis)
	if err != nil {
		return 0, false
	}
	genesis := flattenGroups(genesisGroups)
	clockings, err := enumerateClockings(domains, constraintsForEdges(cs, edges), 200000, 4096)
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := buildScenarioGraphs(cs, edges, accept, genesis, clockings)
	if err != nil {
		return 0, false
	}
	best := cs.len() + 1
	limit := uint64(1) << uint(cs.len())
	for mask := uint64(0); mask < limit; mask++ {
		ok := true
		for i := range scenarios {
			if !scenarioConnected(mask, &scenarios[i]) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		n := 0
		for v := mask; v != 0; v &= v - 1 {
			n++
		}
		if n < best {
			best = n
		}
	}
	if best > cs.len() {
		return 0, false
	}
	return best, true
}

func TestMinimumMatchesExhaustiveEnumeration(t *testing.T) {
	hiddenAr, _, _, _ := hiddenBatchArchive(t)
	cases := []struct {
		name string
		a    *Archive
	}{
		{"temporal adjacency", temporalArchive()},
		{"shared predecessor", sharedPredecessorArchive()},
		{"service key rotation", rotationArchive()},
		{"different clockings", differentClockingArchive()},
		{"hidden union", hiddenAr},
		{"combined", combinedArchive()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := bruteMinimum(t, tc.a)
			if !ok {
				t.Fatal("full archive has no chain")
			}
			c, err := BuildCertificate(tc.a, nil)
			if err != nil {
				t.Fatalf("BuildCertificate: %v", err)
			}
			if c.EvidenceCount != want {
				t.Fatalf("BuildCertificate count = %d, exhaustive minimum = %d", c.EvidenceCount, want)
			}
			if err := c.VerifyAll(nil); err != nil {
				t.Fatalf("VerifyAll: %v", err)
			}
		})
	}
}

func temporalArchive() *Archive {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 10, "record-a", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Exact(10))}
	return &Archive{Nodes: []Node{g, a}, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("a")}}
}

func sharedPredecessorArchive() *Archive {
	g := mkNode("g", "svc", 0, "genesis", Exact(0))
	a := mkNode("a", "svc", 5, "record-a", Exact(0))
	b := mkNode("b", "svc", 6, "record-b", Exact(0))
	a.Renews = []Renewal{renew("g", g.Content, Exact(5))}
	b.Renews = []Renewal{renew("g", g.Content, Exact(6))}
	return &Archive{
		Nodes:      []Node{g, a, b},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("a"), RefNode("b")},
	}
}

func rotationArchive() *Archive {
	g := mkNode("key-1-node", "permanent-service", 0, "genesis", Between(0, 3))
	g.Key = "key-1"
	a := mkNode("key-2-node", "permanent-service", 10, "record-a", Between(2, 5))
	a.Key = "key-2"
	a.Renews = []Renewal{renew("key-1-node", g.Content, Exact(10))}
	return &Archive{
		Nodes:      []Node{g, a},
		Genesis:    []Reference{RefNode("key-1-node")},
		Acceptance: []Reference{RefNode("key-2-node")},
	}
}

func differentClockingArchive() *Archive {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	x := mkNode("x", "signer-a", 50, "branch-x", Exact(0))
	x.Source = "clock-a"
	x.Renews = []Renewal{renew("g", g.Content, Exact(50))}
	y := mkNode("y", "signer-a", 55, "branch-y", Exact(0))
	y.Source = "clock-a"
	y.Renews = []Renewal{renew("g", g.Content, Exact(55))}
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"
	acc.Renews = []Renewal{
		renew("x", x.Content, Between(50, 54)),
		renew("y", y.Content, Between(50, 55)),
	}
	return &Archive{
		Nodes:      []Node{g, x, y, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
	}
}

func combinedArchive() *Archive {
	g := mkNode("g", "signer-a", 0, "genesis", Exact(0))
	g.Source = "clock-a"
	n1 := mkNode("n1", "signer-a", 10, "n1", Exact(0))
	n1.Source = "clock-a"
	n1.Renews = []Renewal{renew("g", g.Content, Exact(10))}
	n2 := mkNode("n2", "signer-a", 20, "n2", Exact(0))
	n2.Source = "clock-a"
	n2.Renews = []Renewal{renew("n1", n1.Content, Exact(10))}
	n3 := mkNode("n3", "signer-a", 30, "n3", Exact(0))
	n3.Source = "clock-a"
	n3.Key = "rotated-key"
	n3.Renews = []Renewal{renew("n2", n2.Content, Exact(10))}
	x := mkNode("x", "signer-a", 50, "x", Exact(0))
	x.Source = "clock-a"
	x.Renews = []Renewal{renew("n3", n3.Content, Exact(20))}
	y := mkNode("y", "signer-a", 55, "y", Exact(0))
	y.Source = "clock-a"
	y.Renews = []Renewal{renew("n3", n3.Content, Exact(25))}
	c1 := cmt("hidden-one")
	c2 := cmt("hidden-two")
	batch := cmt("all-hidden-early")
	acc := mkNode("acc", "signer-b", 100, "acceptance", Between(0, 10))
	acc.Source = "clock-b"
	acc.Renews = []Renewal{
		renew("x", x.Content, Between(50, 54)),
		renew("y", y.Content, Between(50, 55)),
		{OldCommitment: c1, Gap: Between(50, 54)},
		{OldCommitment: c2, Gap: Between(50, 55)},
	}
	acc.Commits = []CommitmentLink{
		{Commitment: batch, Gap: Between(50, 60)},
	}
	return &Archive{
		Nodes:      []Node{g, n1, n2, n3, x, y, acc},
		Genesis:    []Reference{RefNode("g")},
		Acceptance: []Reference{RefNode("acc")},
		Shared: []SharedEvidence{
			{
				Commitment: c1, Time: 50, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(50))},
				Covers:      []NodeID{"hidden-one-record"},
			},
			{
				Commitment: c2, Time: 55, Source: "clock-a", Offset: Exact(0),
				RenewsNodes: []Renewal{renew("g", g.Content, Exact(55))},
				Covers:      []NodeID{"hidden-two-record"},
			},
			{
				Commitment: batch, Time: 50, Source: "clock-a", Offset: Exact(0),
				RenewsNodes:  []Renewal{renew("g", g.Content, Exact(50))},
				Covers:       []NodeID{"hidden-one-record", "hidden-two-record"},
				LicensedBy:   "acc",
				LicenseProof: []byte("batch-proof"),
			},
		},
	}
}

func TestCombinedCasesPreferPermittedBatch(t *testing.T) {
	a := combinedArchive()
	for _, off := range []int64{0, 4, 5, 10} {
		ok, err := a.SecureAt(Clocking{"clock-a": 0, "clock-b": off})
		if err != nil || !ok {
			t.Fatalf("host-side secure at %d: ok=%v err=%v", off, ok, err)
		}
	}
	c := mustBuild(t, a)
	if c.EvidenceCount != 3 {
		t.Fatalf("evidence count = %d, want 3", c.EvidenceCount)
	}
	for _, e := range c.Entries {
		if e.Shared != nil && e.Shared.Commitment != cmt("all-hidden-early") {
			t.Fatalf("unexpected shared entry %s", e.Shared.Commitment)
		}
	}
	for _, off := range []int64{0, 4, 5, 10} {
		if _, err := c.Verify(Clocking{"clock-a": 0, "clock-b": off}); err != nil {
			t.Fatalf("Verify %d: %v", off, err)
		}
	}
}

func TestExhaustiveSmallGraphEdgeSubsets(t *testing.T) {
	// Four records at increasing times, all on one exact clock source.  The
	// six possible renewal edges are enumerated exhaustively.
	base := func() ([]Node, map[[2]string]Renewal) {
		g := mkNode("g", "svc", 0, "g", Exact(0))
		a := mkNode("a", "svc", 5, "a", Exact(0))
		b := mkNode("b", "svc", 9, "b", Exact(0))
		acc := mkNode("t", "svc", 12, "t", Exact(0))
		nodes := []Node{g, a, b, acc}
		rn := map[[2]string]Renewal{
			{"t", "g"}: renew("g", g.Content, Exact(12)),
			{"t", "a"}: renew("a", a.Content, Exact(7)),
			{"t", "b"}: renew("b", b.Content, Exact(3)),
			{"a", "g"}: renew("g", g.Content, Exact(5)),
			{"b", "g"}: renew("g", g.Content, Exact(9)),
			{"b", "a"}: renew("a", a.Content, Exact(4)),
		}
		return nodes, rn
	}
	keys := [][2]string{{"t", "g"}, {"t", "a"}, {"t", "b"}, {"a", "g"}, {"b", "g"}, {"b", "a"}}

	for bits := 0; bits < 1<<len(keys); bits++ {
		nodes, renewals := base()
		index := map[string]int{"g": 0, "a": 1, "b": 2, "t": 3}
		for i, k := range keys {
			if bits&(1<<i) == 0 {
				continue
			}
			j := index[k[0]]
			nodes[j].Renews = append(nodes[j].Renews, renewals[k])
		}
		a := &Archive{Nodes: nodes, Genesis: []Reference{RefNode("g")}, Acceptance: []Reference{RefNode("t")}}
		if err := a.Validate(); err != nil {
			t.Fatalf("bits %06b: Validate: %v", bits, err)
		}
		want, secure := bruteMinimum(t, a)
		c, err := BuildCertificate(a, nil)
		if !secure {
			if !errors.Is(err, ErrNoCertificate) {
				t.Fatalf("bits %06b: err = %v, want ErrNoCertificate", bits, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("bits %06b: BuildCertificate: %v", bits, err)
		}
		if c.EvidenceCount != want {
			t.Fatalf("bits %06b: count = %d, want %d", bits, c.EvidenceCount, want)
		}
		if err := c.VerifyAll(nil); err != nil {
			t.Fatalf("bits %06b: VerifyAll: %v", bits, err)
		}
	}
}
