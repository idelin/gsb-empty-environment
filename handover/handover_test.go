package handover

import (
	"strconv"
	"testing"
)

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

func seq(lo, hi uint64) []uint64 {
	out := make([]uint64, 0, hi-lo)
	for p := lo; p < hi; p++ {
		out = append(out, p)
	}
	return out
}

// TestCrossDeviceAlias checks that the same physical pages visible through two
// device windows are blocked by either device's lease, and that only an
// exact-identity confirmation clears it.
func TestCrossDeviceAlias(t *testing.T) {
	segA := Segment{LogBase: 0, PhysBase: 0x1000, Perm: IdentityPerm(3)}
	segB := Segment{LogBase: 0x200, PhysBase: 0x1000, Perm: IdentityPerm(3)}
	ownerA := id("devA", 1, 7, "rxq")
	ownerB := id("devB", 1, 7, "rxq")
	requested := mustNormalize(t, []Interval{{0, 8}})

	newManifest := func(conf *Confirmation) *Manifest {
		m := &Manifest{
			Windows: []Window{
				{Owner: ownerA, Segments: []Segment{segA}},
				{Owner: ownerB, Segments: []Segment{segB}},
			},
			Leases: []Lease{
				{ID: "L-B", Owner: ownerB, Perm: PermWrite, Pages: mustNormalize(t, []Interval{{0x1000, 0x1008}})},
			},
		}
		if conf != nil {
			m.Confirmations = []Confirmation{*conf}
		}
		return m
	}

	t.Run("alias reported and blocked without confirmation", func(t *testing.T) {
		m := newManifest(nil)
		aliases, err := m.AliasWindows(mustNormalize(t, []Interval{{0x1000, 0x1008}}))
		if err != nil {
			t.Fatalf("AliasWindows: %v", err)
		}
		if len(aliases) != 2 || aliases[0] != ownerA || aliases[1] != ownerB {
			t.Fatalf("expected both owners as aliases, got %+v", aliases)
		}
		cred, err := Issue(m, HandoverRequest{WindowOwner: ownerA, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if !cred.SafeLogical.IsEmpty() {
			t.Fatalf("alias lease must block the whole request, got safe %v", cred.SafeLogical)
		}
		assertPages(t, cred.SafePhysical, nil)
		if len(cred.Gaps) != 1 || cred.Gaps[0].Owner != ownerB || cred.Gaps[0].Perm != PermWrite {
			t.Fatalf("expected one devB write gap, got %+v", cred.Gaps)
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("exact cross-device confirmation clears the alias", func(t *testing.T) {
		conf := &Confirmation{
			ID:    "C-B",
			Owner: ownerB,
			Perm:  PermWrite,
			Pages: mustNormalize(t, []Interval{{0x1000, 0x1008}}),
		}
		m := newManifest(conf)
		cred, err := Issue(m, HandoverRequest{WindowOwner: ownerA, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		assertPages(t, cred.SafeLogical, seq(0, 8))
		if len(cred.Gaps) != 0 {
			t.Fatalf("expected no gaps, got %+v", cred.Gaps)
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("confirmation from the other device never clears", func(t *testing.T) {
		conf := &Confirmation{
			ID:    "C-A-WRONG-DEVICE",
			Owner: ownerA,
			Perm:  PermWrite,
			Pages: mustNormalize(t, []Interval{{0x1000, 0x1008}}),
		}
		m := newManifest(conf)
		cred, err := Issue(m, HandoverRequest{WindowOwner: ownerA, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if !cred.SafeLogical.IsEmpty() {
			t.Fatalf("wrong-device confirmation must not clear the lease")
		}
		if len(cred.ConfirmationIDs) != 0 {
			t.Fatalf("irrelevant confirmation must not be referenced, got %v", cred.ConfirmationIDs)
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("partial confirmation yields a page-accurate subset", func(t *testing.T) {
		conf := &Confirmation{
			ID:    "C-B-PARTIAL",
			Owner: ownerB,
			Perm:  PermWrite,
			Pages: mustNormalize(t, []Interval{{0x1003, 0x1006}}),
		}
		m := newManifest(conf)
		cred, err := Issue(m, HandoverRequest{WindowOwner: ownerA, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		assertPages(t, cred.SafeLogical, []uint64{3, 4, 5})
		assertPages(t, cred.SafePhysical, []uint64{0x1003, 0x1004, 0x1005})
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})
}

// TestBitPermutationScatter checks a bit-reversal window where logical pages
// are contiguous but their physical images are discrete, against a page-by-page
// oracle, including sub-range requests.
func TestBitPermutationScatter(t *testing.T) {
	reverse := Perm{N: 8, Map: []uint{7, 6, 5, 4, 3, 2, 1, 0}}
	if err := reverse.Validate(); err != nil {
		t.Fatalf("reverse perm invalid: %v", err)
	}
	seg := Segment{LogBase: 0x400, PhysBase: 0x8000, Perm: reverse}
	owner := id("nic", 2, 3, "dq")
	m := &Manifest{Windows: []Window{{Owner: owner, Segments: []Segment{seg}}}}

	t.Run("full window image matches per-page oracle", func(t *testing.T) {
		requested := mustNormalize(t, []Interval{{0x400, 0x500}})
		cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		// A bijection over the whole domain covers the entire segment range.
		assertPages(t, cred.SafePhysical, seq(0x8000, 0x8100))
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})

	t.Run("logically contiguous sub-request maps to discrete pages", func(t *testing.T) {
		requested := mustNormalize(t, []Interval{{0x400, 0x410}}) // local 0..15
		cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		wantLocal := IntervalSet{{0, 16}}
		wantPhysical := mustNormalize(t, nil)
		for _, page := range toPages(t, refImage(t, reverse, wantLocal)) {
			wantPhysical = Union(wantPhysical, pageSet(0x8000+page))
		}
		assertPages(t, cred.SafePhysical, toPages(t, wantPhysical))
		assertPages(t, cred.SafeLogical, seq(0x400, 0x410))
		// Local pages 0..15 keep the low four bits zero; bit reversal pushes
		// them to the high four bits, producing discrete, strided intervals.
		if len(cred.SafePhysical) != len(wantPhysical) {
			t.Fatalf("expected %d discrete physical intervals, got %d", len(wantPhysical), len(cred.SafePhysical))
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})
}

// TestAlternatingPageConfirmations checks confirmations covering only even
// pages: the credential must return exactly those pages rather than the whole
// aligned region.
func TestAlternatingPageConfirmations(t *testing.T) {
	owner := id("nvme", 1, 1, "ioq")
	seg := Segment{LogBase: 0, PhysBase: 0x2000, Perm: IdentityPerm(3)}
	lease := Lease{
		ID:    "L-RW",
		Owner: owner,
		Perm:  PermRead | PermWrite,
		Pages: mustNormalize(t, []Interval{{0x2000, 0x2008}}),
	}
	confirmations := make([]Confirmation, 0, 4)
	for page := uint64(0x2000); page < 0x2008; page += 2 {
		confirmations = append(confirmations, Confirmation{
			ID:    "C-EVEN-" + itoa(page),
			Owner: owner,
			Perm:  PermRead | PermWrite,
			Pages: pageSet(page),
		})
	}
	m := &Manifest{
		Windows:       []Window{{Owner: owner, Segments: []Segment{seg}}},
		Leases:        []Lease{lease},
		Confirmations: confirmations,
	}
	cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: mustNormalize(t, []Interval{{0, 8}})})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	assertPages(t, cred.SafeLogical, []uint64{0, 2, 4, 6})
	assertPages(t, cred.SafePhysical, []uint64{0x2000, 0x2002, 0x2004, 0x2006})
	if len(cred.Gaps) != 2 {
		t.Fatalf("expected separate read and write gaps for odd pages, got %d", len(cred.Gaps))
	}
	for _, gap := range cred.Gaps {
		assertPages(t, gap.Logical, []uint64{1, 3, 5, 7})
		assertPages(t, gap.Physical, []uint64{0x2001, 0x2003, 0x2005, 0x2007})
	}
	if err := Verify(m, cred); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// A read-only confirmation cannot retire the write half of the lease.
	ro := Confirmation{ID: "C-RO", Owner: owner, Perm: PermRead, Pages: mustNormalize(t, []Interval{{0x2000, 0x2008}})}
	m2 := &Manifest{
		Windows:       []Window{{Owner: owner, Segments: []Segment{seg}}},
		Leases:        []Lease{lease},
		Confirmations: []Confirmation{ro},
	}
	cred2, err := Issue(m2, HandoverRequest{WindowOwner: owner, Requested: mustNormalize(t, []Interval{{0, 8}})})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !cred2.SafeLogical.IsEmpty() {
		t.Fatalf("read-only confirmation must leave the write lease blocking everything")
	}
	if len(cred2.Gaps) != 1 || cred2.Gaps[0].Perm != PermWrite {
		t.Fatalf("expected one write gap, got %+v", cred2.Gaps)
	}
}

// TestResetSameNameQueue checks that a queue re-created after a device reset
// (same permanent id and queue name, new boot generation) cannot use its
// confirmation to retire a pre-reset lease, and likewise for domain changes.
func TestResetSameNameQueue(t *testing.T) {
	seg := Segment{LogBase: 0, PhysBase: 0x3000, Perm: IdentityPerm(2)}
	oldID := id("dev", 4, 9, "q0")
	resetID := id("dev", 5, 9, "q0")
	requested := mustNormalize(t, []Interval{{0, 4}})
	leaseRange := mustNormalize(t, []Interval{{0x3000, 0x3004}})

	t.Run("boot generation mismatch after reset", func(t *testing.T) {
		m := &Manifest{
			Windows: []Window{{Owner: resetID, Segments: []Segment{seg}}},
			Leases: []Lease{{
				ID: "L-OLD", Owner: oldID, Perm: PermWrite, Pages: leaseRange,
			}},
			Confirmations: []Confirmation{{
				ID: "C-NEW-BOOT", Owner: resetID, Perm: PermWrite, Pages: leaseRange,
			}},
		}
		cred, err := Issue(m, HandoverRequest{WindowOwner: resetID, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if !cred.SafeLogical.IsEmpty() {
			t.Fatalf("post-reset queue must not retire a pre-reset lease")
		}
		if len(cred.Gaps) != 1 || cred.Gaps[0].Owner != oldID {
			t.Fatalf("expected gap attributed to the old boot tuple, got %+v", cred.Gaps)
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify: %v", err)
		}

		// The same-boot confirmation does retire it.
		m.Confirmations[0].Owner = oldID
		cred, err = Issue(m, HandoverRequest{WindowOwner: resetID, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		assertPages(t, cred.SafeLogical, seq(0, 4))
	})

	t.Run("domain generation mismatch", func(t *testing.T) {
		newDomain := id("dev", 4, 10, "q0")
		m := &Manifest{
			Windows: []Window{{Owner: newDomain, Segments: []Segment{seg}}},
			Leases: []Lease{{
				ID: "L-OLD-DOMAIN", Owner: oldID, Perm: PermRead, Pages: leaseRange,
			}},
			Confirmations: []Confirmation{{
				ID: "C-NEW-DOMAIN", Owner: newDomain, Perm: PermRead, Pages: leaseRange,
			}},
		}
		cred, err := Issue(m, HandoverRequest{WindowOwner: newDomain, Requested: requested})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}
		if !cred.SafeLogical.IsEmpty() {
			t.Fatalf("cross-domain confirmation must not retire the old-domain read lease")
		}
		if len(cred.Gaps) != 1 || cred.Gaps[0].Perm != PermRead {
			t.Fatalf("expected one read gap, got %+v", cred.Gaps)
		}
	})
}
