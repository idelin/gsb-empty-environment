package netsec

import (
	"errors"
	"testing"
)

func mustPage(t *testing.T, m *Memory, ref PageRef) Page {
	t.Helper()
	page, ok := m.Page(ref)
	if !ok {
		t.Fatalf("page %v does not resolve", ref)
	}
	return page
}

func create(t *testing.T, m *Memory, domain DomainID, content string, shareable bool, tags ...string) PageRef {
	t.Helper()
	ref, err := m.CreatePageE(PageSpec{
		DomainID:  domain,
		Content:   []byte(content),
		Shareable: shareable,
		Tags:      tags,
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	return ref
}

func TestDefaultPolicyMergesSameDomainCleanSharedPages(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d1", "same", true)
	b := create(t, m, "d1", "same", true)

	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID != pb.PhysicalPageID {
		t.Fatalf("pages were not merged: %d != %d", pa.PhysicalPageID, pb.PhysicalPageID)
	}
	if pa.RefCount != 2 || pb.RefCount != 2 {
		t.Fatalf("unexpected refcounts: %d, %d", pa.RefCount, pb.RefCount)
	}
	if stats := m.Stats(); stats.Merges != 1 || stats.MergedPages != 1 {
		t.Fatalf("unexpected merge stats: %+v", stats)
	}
}

func TestDefaultPolicyRejectsCrossDomainSensitiveAndUnshareable(t *testing.T) {
	cases := []struct {
		name string
		a, b PageSpec
	}{
		{
			name: "cross-domain",
			a:    PageSpec{DomainID: "d1", Content: []byte("x"), Shareable: true},
			b:    PageSpec{DomainID: "d2", Content: []byte("x"), Shareable: true},
		},
		{
			name: "sensitive-tag",
			a:    PageSpec{DomainID: "d1", Content: []byte("x"), Shareable: true, Tags: []string{"secret"}},
			b:    PageSpec{DomainID: "d1", Content: []byte("x"), Shareable: true},
		},
		{
			name: "unshareable",
			a:    PageSpec{DomainID: "d1", Content: []byte("x")},
			b:    PageSpec{DomainID: "d1", Content: []byte("x"), Shareable: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemory()
			a, err := m.CreatePageE(tc.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := m.CreatePageE(tc.b)
			if err != nil {
				t.Fatal(err)
			}
			pa, pb := mustPage(t, m, a), mustPage(t, m, b)
			if pa.PhysicalPageID == pb.PhysicalPageID {
				t.Fatalf("unexpected merge under default policy")
			}
		})
	}
}

func TestGlobalPolicyAllowsCrossDomainMerge(t *testing.T) {
	m := NewMemory(Config{Policy: GlobalDedupPolicy{}})
	a := create(t, m, "d1", "x", true)
	b := create(t, m, "d2", "x", true)
	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID != pb.PhysicalPageID {
		t.Fatal("global policy did not merge cross-domain pages")
	}
}

func TestPolicyCallbackDecidesMerge(t *testing.T) {
	calls := 0
	m := NewMemory(Config{Policy: PolicyFunc(func(_, _ Page) bool {
		calls++
		return false
	})})
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)
	if calls == 0 {
		t.Fatal("policy callback was not consulted")
	}
	if mustPage(t, m, a).PhysicalPageID == mustPage(t, m, b).PhysicalPageID {
		t.Fatal("policy callback denial was ignored")
	}
}

func TestConfigAcceptsCallbackLiteral(t *testing.T) {
	m := NewMemory(Config{Policy: func(incoming, resident Page) bool { return false }})
	a := create(t, m, "d1", "x", true)
	b := create(t, m, "d1", "x", true)
	if mustPage(t, m, a).PhysicalPageID == mustPage(t, m, b).PhysicalPageID {
		t.Fatal("callback literal denial was ignored")
	}
}

func TestConservativePolicySeesAggregateForeignOrSensitiveMembers(t *testing.T) {
	// A permissive policy can create a physical page with foreign, tagged and
	// unshareable members.  After switching back to the conservative policy,
	// a clean incoming page must not join that physical page just because its
	// lowest member happens to be clean.
	m := NewMemory(Config{Policy: GlobalDedupPolicy{}})
	a := create(t, m, "d1", "x", true)
	b := create(t, m, "d2", "x", true, "secret")
	c := create(t, m, "d1", "x", false)
	if mustPage(t, m, a).PhysicalPageID != mustPage(t, m, b).PhysicalPageID ||
		mustPage(t, m, a).PhysicalPageID != mustPage(t, m, c).PhysicalPageID {
		t.Fatal("permissive policy did not build the mixed physical page")
	}

	m.SetPolicy(ConservativePolicy{})
	d := create(t, m, "d1", "x", true)
	if mustPage(t, m, d).PhysicalPageID == mustPage(t, m, a).PhysicalPageID {
		t.Fatal("clean page joined a physical page with foreign/sensitive members")
	}
}

func TestFirstWriteCOWAndLaterPrivateWrite(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "shared", true)
	b := create(t, m, "d", "shared", true)
	before := mustPage(t, m, a)

	result, err := m.Write(a, []byte("shared"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.COW || result.COWFailed {
		t.Fatalf("expected successful COW: %+v", result)
	}
	if result.Cost != FixedCost(10) {
		t.Fatalf("COW cost = %v, want 10", result.Cost)
	}
	after := mustPage(t, m, a)
	if after.PhysicalPageID == before.PhysicalPageID {
		t.Fatal("COW did not detach the virtual page")
	}
	if after.RefCount != 1 || mustPage(t, m, b).RefCount != 1 {
		t.Fatal("unexpected refcounts after COW")
	}

	result, err = m.Write(a, []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	if result.COW || result.Cost != FixedCost(1) {
		t.Fatalf("expected private write cost 1, got %+v", result)
	}
	if stats := m.Stats(); stats.COWEvents != 1 || stats.PrivateWrites != 1 {
		t.Fatalf("unexpected write stats: %+v", stats)
	}
}

func TestReadUsesFixedAccessCost(t *testing.T) {
	access := Interval(7, 9)
	m := NewMemory(Config{Costs: CostModel{Access: access}})
	a := create(t, m, "d", "x", true)
	result, err := m.Read(a)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost != access || result.Observation.Event != EventAccess {
		t.Fatalf("unexpected read result: %+v", result)
	}
}

func TestCallerSuppliedIntervalsForEveryEvent(t *testing.T) {
	costs := CostModel{
		Access:       Interval(5, 6),
		COW:          Interval(11, 13),
		PrivateWrite: Interval(2, 4),
		COWFailure:   Interval(7, 8),
	}
	m := NewMemory(Config{Costs: costs})
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)

	read, err := m.Read(a)
	if err != nil {
		t.Fatal(err)
	}
	if read.Cost != Interval(5, 6) {
		t.Fatalf("access cost = %v", read.Cost)
	}

	m.FailNextCOW()
	failed, err := m.Write(a, []byte("y"))
	if !errors.Is(err, ErrCOWFailed) {
		t.Fatalf("error = %v", err)
	}
	if failed.Cost != Interval(7, 8) {
		t.Fatalf("failure cost = %v", failed.Cost)
	}

	cow, err := m.Write(a, []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if !cow.COW || cow.Cost != Interval(11, 13) {
		t.Fatalf("COW result = %+v", cow)
	}
	private, err := m.Write(a, []byte("z"))
	if err != nil {
		t.Fatal(err)
	}
	if private.COW || private.Cost != Interval(2, 4) {
		t.Fatalf("private result = %+v", private)
	}
	_ = b
}

func TestUpdateContentDetachesAndChangesDigest(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "old", true)
	b := create(t, m, "d", "old", true)
	if mustPage(t, m, a).PhysicalPageID != mustPage(t, m, b).PhysicalPageID {
		t.Fatal("setup did not merge")
	}

	result, err := m.UpdateContent(a, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.COW || result.Cost != FixedCost(10) {
		t.Fatalf("unexpected update result: %+v", result)
	}
	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID == pb.PhysicalPageID {
		t.Fatal("content update did not detach")
	}
	if pa.Digest == pb.Digest {
		t.Fatal("content update did not recompute digest")
	}
	if string(pa.Content) != "new" || string(pb.Content) != "old" {
		t.Fatalf("unexpected contents %q and %q", pa.Content, pb.Content)
	}

	// The update itself detached; a later clean page is free to merge with the
	// newly written content, which is ordinary deduplication of a live page.
	c := create(t, m, "d", "new", true)
	if mustPage(t, m, c).PhysicalPageID != pa.PhysicalPageID {
		t.Fatal("a later identical clean page did not deduplicate")
	}
}

func TestUpdateTagsDetachesBeforeWrite(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)

	result, err := m.AddTag(a, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !result.COW || result.Cost != FixedCost(10) {
		t.Fatalf("tag update did not report COW: %+v", result)
	}
	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID == pb.PhysicalPageID {
		t.Fatal("tag update did not detach")
	}
	if !pa.Tags.Has("secret") || !pa.SensitiveTags.Has("secret") {
		t.Fatal("tag was not installed")
	}

	c := create(t, m, "d", "x", true)
	if mustPage(t, m, c).PhysicalPageID == pa.PhysicalPageID {
		t.Fatal("sensitive page was merged with a new clean page")
	}
}

func TestClearingShareableDetaches(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)
	if _, err := m.SetShareable(a, false); err != nil {
		t.Fatal(err)
	}
	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID == pb.PhysicalPageID || pa.Shareable {
		t.Fatal("clearing shareable did not detach")
	}
}

func TestDigestCollisionAliasMerges(t *testing.T) {
	const collision Digest = "collision"
	m := NewMemory(Config{Policy: GlobalDedupPolicy{}})
	a, err := m.CreatePageE(PageSpec{DomainID: "d1", Content: []byte("alice"), Digest: collision, Shareable: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreatePageE(PageSpec{DomainID: "d2", Content: []byte("bob"), Digest: collision, Shareable: true})
	if err != nil {
		t.Fatal(err)
	}
	pa, pb := mustPage(t, m, a), mustPage(t, m, b)
	if pa.PhysicalPageID != pb.PhysicalPageID {
		t.Fatal("digest collision was not aliased")
	}
	result, err := m.Write(a, []byte("alice!"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.COW {
		t.Fatal("collision alias did not produce COW")
	}
}

func TestCustomDigestFunctionCanModelCollision(t *testing.T) {
	m := NewMemory(Config{
		Policy:     GlobalDedupPolicy{},
		DigestFunc: func([]byte) Digest { return "always-the-same" },
	})
	a := create(t, m, "d1", "alice", true)
	b := create(t, m, "d2", "bob", true)
	if mustPage(t, m, a).PhysicalPageID != mustPage(t, m, b).PhysicalPageID {
		t.Fatal("custom colliding digest function did not alias pages")
	}
}

func TestCOWFailureLeavesStateIntact(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)
	beforeA, beforeB := mustPage(t, m, a), mustPage(t, m, b)

	m.FailNextCOW()
	result, err := m.Write(a, []byte("changed"))
	if !errors.Is(err, ErrCOWFailed) {
		t.Fatalf("error = %v, want ErrCOWFailed", err)
	}
	if !result.COWFailed || result.Cost != FixedCost(10) {
		t.Fatalf("unexpected failure result: %+v", result)
	}
	afterA, afterB := mustPage(t, m, a), mustPage(t, m, b)
	if afterA.PhysicalPageID != beforeA.PhysicalPageID || afterB.PhysicalPageID != beforeB.PhysicalPageID {
		t.Fatal("COW failure changed physical mappings")
	}
	if afterA.RefCount != 2 || afterB.RefCount != 2 {
		t.Fatal("COW failure changed refcounts")
	}
	if string(afterA.Content) != "x" {
		t.Fatal("COW failure changed content")
	}
	if stats := m.Stats(); stats.COWFailures != 1 || stats.COWEvents != 0 {
		t.Fatalf("unexpected failure stats: %+v", stats)
	}

	if _, err := m.Write(a, []byte("changed")); err != nil {
		t.Fatalf("retry after injected failure: %v", err)
	}
}

func TestRefCountReclaimsPhysicalPage(t *testing.T) {
	m := NewMemory()
	a := create(t, m, "d", "x", true)
	b := create(t, m, "d", "x", true)
	phys := mustPage(t, m, a).PhysicalPageID

	if err := m.Delete(a); err != nil {
		t.Fatal(err)
	}
	if m.PhysicalRefCount(phys) != 1 {
		t.Fatalf("refcount = %d, want 1", m.PhysicalRefCount(phys))
	}
	if _, ok := m.PhysicalPage(phys); !ok {
		t.Fatal("physical page was reclaimed while still referenced")
	}

	if err := m.Delete(b); err != nil {
		t.Fatal(err)
	}
	if m.PhysicalRefCount(phys) != 0 {
		t.Fatalf("refcount = %d, want 0", m.PhysicalRefCount(phys))
	}
	if _, ok := m.PhysicalPage(phys); ok {
		t.Fatal("physical page survived zero refcount")
	}
	if stats := m.Stats(); stats.LivePhysicalPages != 0 || stats.ReclaimedPages == 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestAddressReuseCarriesGeneration(t *testing.T) {
	m := NewMemory()
	first := create(t, m, "d1", "one", true)
	firstPage := mustPage(t, m, first)
	if err := m.Delete(first); err != nil {
		t.Fatal(err)
	}

	second := create(t, m, "d2", "two", true)
	secondPage := mustPage(t, m, second)
	if second.ID != first.ID {
		t.Fatalf("slot was not reused: %d != %d", second.ID, first.ID)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("generation did not advance: %d <= %d", second.Generation, first.Generation)
	}
	if secondPage.PhysicalPageID != firstPage.PhysicalPageID {
		t.Logf("physical slot was not reused immediately; that is allowed")
	} else if secondPage.PhysicalPageGeneration <= firstPage.PhysicalPageGeneration {
		t.Fatalf("physical generation did not advance: %d <= %d",
			secondPage.PhysicalPageGeneration, firstPage.PhysicalPageGeneration)
	}
	if err := m.Resolve(first); !errors.Is(err, ErrStalePage) {
		t.Fatalf("stale ref resolved: %v", err)
	}
	if _, err := m.Write(first, []byte("stale")); !errors.Is(err, ErrStalePage) {
		t.Fatalf("stale write error = %v", err)
	}
}

func TestCostIntervalOverlap(t *testing.T) {
	if !Interval(8, 12).Overlaps(Interval(10, 14)) {
		t.Fatal("expected overlapping intervals")
	}
	if Interval(1, 9).Overlaps(Interval(10, 10)) {
		t.Fatal("expected disjoint intervals")
	}
}
