package certcache

import "testing"

// buildDiamond creates:
//
//	a -> b (1), a -> c (5), b -> d (1), c -> d (1), b -> c (1)
func buildDiamond(t *testing.T) (*Graph, *Cache) {
	g := NewGraph(0)
	c := NewCache(g)
	edges := []struct {
		from, to string
		w        int64
	}{
		{"a", "b", 1},
		{"a", "c", 5},
		{"b", "d", 1},
		{"c", "d", 1},
		{"b", "c", 1},
	}
	for _, e := range edges {
		if err := c.AddEdge(e.from, e.to, e.w); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e.from, e.to, err)
		}
	}
	return g, c
}

func TestCertificateRecordsDependencyVersions(t *testing.T) {
	g, c := buildDiamond(t)

	ok, cert := c.Reachable("a", "d")
	if !ok {
		t.Fatal("expected a->d reachable")
	}
	if len(cert.Deps) == 0 {
		t.Fatal("reachability certificate has no dependencies")
	}
	for _, dep := range cert.Deps {
		edge := g.Edge(dep.Edge.From, dep.Edge.To)
		if edge == nil {
			t.Fatalf("dependency %v missing from graph", dep.Edge)
		}
		if edge.Version != dep.Version {
			t.Fatalf("dependency %v: cert version %d, graph version %d",
				dep.Edge, dep.Version, edge.Version)
		}
	}

	dist, ok, scert := c.Shortest("a", "d")
	if !ok || dist != 2 {
		t.Fatalf("expected shortest a->d = 2, got %d (ok=%v)", dist, ok)
	}
	if scert.AddEpoch != g.AddEpoch() {
		t.Fatalf("shortest cert epoch %d, graph epoch %d", scert.AddEpoch, g.AddEpoch())
	}
	if !Validate(g, cert) || !Validate(g, scert) {
		t.Fatal("fresh certificates must validate")
	}
}

func TestRemoveEdgeInvalidatesOnlyDependents(t *testing.T) {
	g, c := buildDiamond(t)

	// a->d shortest path is a->b->d = 2 (depends on b->d);
	// c->d shortest path is c->d (does not).
	mustShortest(t, c, "a", "d", 2)
	indepCert := c.Cached(KindShortestPath, "c", "d")
	if indepCert == nil {
		if _, ok, _ := c.Shortest("c", "d"); !ok {
			t.Fatal("c->d should be reachable")
		}
		indepCert = c.Cached(KindShortestPath, "c", "d")
	}
	depReach := mustReachCert(t, c, "a", "d")
	indepReach := mustReachCert(t, c, "c", "d")

	if !c.RemoveEdge("b", "d") {
		t.Fatal("expected b->d to exist")
	}

	// Dependent certificates are gone, unrelated ones survive untouched.
	if got := c.Cached(KindShortestPath, "a", "d"); got != nil {
		t.Fatal("dependent shortest cert should be invalidated")
	}
	if got := c.Cached(KindShortestPath, "c", "d"); got != indepCert {
		t.Fatal("unrelated shortest cert should survive")
	}
	if got := c.Cached(KindReachability, "a", "d"); got != nil && got == depReach {
		t.Fatal("dependent reachability cert should be invalidated")
	}
	if got := c.Cached(KindReachability, "c", "d"); got != indepReach {
		t.Fatal("unrelated reachability cert should survive")
	}

	// Full re-verification agrees with the cache after invalidation.
	assertConsistent(t, g, c, "a", "d")
	assertConsistent(t, g, c, "c", "d")
}

func TestSetWeightInvalidatesOnlyDependents(t *testing.T) {
	g, c := buildDiamond(t)

	mustShortest(t, c, "a", "d", 2) // via a->b->d
	mustShortest(t, c, "c", "d", 1)
	indep := c.Cached(KindShortestPath, "c", "d")

	if err := c.SetWeight("b", "d", 10); err != nil {
		t.Fatalf("SetWeight: %v", err)
	}

	if got := c.Cached(KindShortestPath, "a", "d"); got != nil {
		t.Fatal("cert depending on b->d should be invalidated")
	}
	if got := c.Cached(KindShortestPath, "c", "d"); got != indep {
		t.Fatal("unrelated cert should survive weight change")
	}

	// New shortest a->d goes a->b->c->d = 3; cross-check fully.
	assertConsistent(t, g, c, "a", "d")
	mustShortest(t, c, "a", "d", 3)
}

func TestAddEdgeInvalidatesShortestButNotReachability(t *testing.T) {
	g, c := buildDiamond(t)

	mustShortest(t, c, "a", "d", 2)
	reachCert := mustReachCert(t, c, "a", "d")
	mustShortest(t, c, "c", "d", 1)

	if c.Len(KindShortestPath) == 0 || c.Len(KindReachability) == 0 {
		t.Fatal("expected populated caches")
	}

	// A new shortcut makes the old shortest certificates stale.
	if err := c.AddEdge("a", "d", 1); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	if got := c.Len(KindShortestPath); got != 0 {
		t.Fatalf("all shortest certs should be invalidated, %d remain", got)
	}
	if got := c.Cached(KindReachability, "a", "d"); got != reachCert {
		t.Fatal("reachability cert must survive edge insertion")
	}
	if !Validate(g, reachCert) {
		t.Fatal("reachability cert should still validate after insertion")
	}

	// Recomputed shortest path uses the new edge; full check agrees.
	mustShortest(t, c, "a", "d", 1)
	assertConsistent(t, g, c, "a", "d")
}

func TestCertificateTypesManagedSeparately(t *testing.T) {
	_, c := buildDiamond(t)

	mustShortest(t, c, "a", "d", 2)
	mustReachCert(t, c, "a", "d")

	if c.Len(KindShortestPath) != 1 || c.Len(KindReachability) != 1 {
		t.Fatalf("expected 1 cert per type, got shortest=%d reach=%d",
			c.Len(KindShortestPath), c.Len(KindReachability))
	}

	// Removing an edge with dependents in both stores clears both.
	c.RemoveEdge("b", "d")
	if c.Len(KindShortestPath) != 0 || c.Len(KindReachability) != 0 {
		t.Fatal("both per-type stores should drop dependent certs")
	}

	// Insertion empties only the shortest store.
	mustShortest(t, c, "a", "d", 3) // now via a->b->c->d
	mustReachCert(t, c, "a", "d")
	c.AddEdge("x", "y", 1)
	if c.Len(KindShortestPath) != 0 {
		t.Fatal("insertion must clear shortest store")
	}
	if c.Len(KindReachability) != 1 {
		t.Fatal("insertion must not touch reachability store")
	}
}

func TestVersionWraparoundRejected(t *testing.T) {
	g := NewGraph(2) // versions 1 and 2 only
	c := NewCache(g)

	if err := c.AddEdge("a", "b", 1); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := c.SetWeight("a", "b", 2); err != nil {
		t.Fatalf("SetWeight to version 2: %v", err)
	}
	// Third version would wrap the bound: rejected.
	if err := c.SetWeight("a", "b", 3); err != ErrVersionWraparound {
		t.Fatalf("expected ErrVersionWraparound, got %v", err)
	}
	// The rejected mutation must not have changed the edge.
	if e := g.Edge("a", "b"); e.Weight != 2 || e.Version != 2 {
		t.Fatalf("rejected mutation mutated edge: %+v", e)
	}

	// Deleting and re-adding does not reset the counter either.
	if !c.RemoveEdge("a", "b") {
		t.Fatal("expected a->b to exist")
	}
	if err := c.AddEdge("a", "b", 1); err != ErrVersionWraparound {
		t.Fatalf("re-add should hit wraparound, got %v", err)
	}
	if g.Edge("a", "b") != nil {
		t.Fatal("rejected re-add must not recreate the edge")
	}
}

func TestVersionsNeverRecycledAcrossDelete(t *testing.T) {
	g := NewGraph(0)
	c := NewCache(g)

	c.AddEdge("a", "b", 1)
	c.AddEdge("b", "c", 1)
	cert := mustReachCert(t, c, "a", "c")

	// Delete and re-add a->b: it must get a fresh version, so the old
	// certificate stays invalid instead of accidentally matching.
	c.RemoveEdge("a", "b")
	if err := c.AddEdge("a", "b", 1); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if e := g.Edge("a", "b"); e.Version != 2 {
		t.Fatalf("re-added edge should have version 2, got %d", e.Version)
	}
	if Validate(g, cert) {
		t.Fatal("stale certificate must not validate against recycled edge")
	}
}

// TestFullReverificationCrossCheck runs a mutation script and, after every
// step, compares cache answers against from-scratch recomputation for all
// node pairs, and confirms surviving certificates were not recomputed.
func TestFullReverificationCrossCheck(t *testing.T) {
	g, c := buildDiamond(t)
	nodes := []string{"a", "b", "c", "d"}

	prime := func() {
		for _, u := range nodes {
			for _, v := range nodes {
				c.Reachable(u, v)
				c.Shortest(u, v)
			}
		}
	}
	prime()

	type step struct {
		name string
		run  func(t *testing.T)
	}
	steps := []step{
		{"remove b->d", func(t *testing.T) { c.RemoveEdge("b", "d") }},
		{"reweight a->c", func(t *testing.T) {
			if err := c.SetWeight("a", "c", 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"add d->a", func(t *testing.T) {
			if err := c.AddEdge("d", "a", 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"remove missing edge", func(t *testing.T) {
			if c.RemoveEdge("a", "z") {
				t.Fatal("removing a missing edge should report false")
			}
		}},
	}

	for _, s := range steps {
		// Snapshot surviving certificates to verify targeted
		// invalidation keeps them by identity.
		beforeReach := snapshotCerts(c, KindReachability)
		beforeShort := snapshotCerts(c, KindShortestPath)

		s.run(t)

		for q, cert := range beforeReach {
			if got := c.Cached(KindReachability, q.From, q.To); got == cert {
				if !Validate(g, got) {
					t.Fatalf("%s: surviving reach cert %v no longer validates", s.name, q)
				}
			}
		}
		for q, cert := range beforeShort {
			if got := c.Cached(KindShortestPath, q.From, q.To); got == cert {
				if !Validate(g, got) {
					t.Fatalf("%s: surviving shortest cert %v no longer validates", s.name, q)
				}
			}
		}

		for _, u := range nodes {
			for _, v := range nodes {
				assertConsistent(t, g, c, u, v)
			}
		}
	}
}

func snapshotCerts(c *Cache, kind CertKind) map[Query]*Certificate {
	out := make(map[Query]*Certificate)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for q, cert := range c.store(kind) {
		out[q] = cert
	}
	return out
}

func mustShortest(t *testing.T, c *Cache, from, to string, want int64) (int64, bool, *Certificate) {
	dist, ok, cert := c.Shortest(from, to)
	if !ok {
		t.Fatalf("expected %s->%s reachable", from, to)
	}
	if dist != want {
		t.Fatalf("shortest %s->%s = %d, want %d", from, to, dist, want)
	}
	return dist, ok, cert
}

func mustReachCert(t *testing.T, c *Cache, from, to string) *Certificate {
	ok, cert := c.Reachable(from, to)
	if !ok || cert == nil {
		t.Fatalf("expected %s->%s reachable with cert", from, to)
	}
	return cert
}

// assertConsistent cross-checks the cache against full re-verification.
func assertConsistent(t *testing.T, g *Graph, c *Cache, from, to string) {
	wantReach, _ := FullReachable(g, from, to)
	gotReach, reachCert := c.Reachable(from, to)
	if gotReach != wantReach {
		t.Fatalf("reachability %s->%s: cache=%v full=%v", from, to, gotReach, wantReach)
	}
	if gotReach && !Validate(g, reachCert) {
		t.Fatalf("reachability cert %s->%s served by cache fails validation", from, to)
	}

	wantDist, wantOK, _ := FullShortest(g, from, to)
	gotDist, gotOK, shortCert := c.Shortest(from, to)
	if gotOK != wantOK || (wantOK && gotDist != wantDist) {
		t.Fatalf("shortest %s->%s: cache=(%d,%v) full=(%d,%v)",
			from, to, gotDist, gotOK, wantDist, wantOK)
	}
	if gotOK && !Validate(g, shortCert) {
		t.Fatalf("shortest cert %s->%s served by cache fails validation", from, to)
	}
}
