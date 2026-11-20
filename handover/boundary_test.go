package handover

import "testing"

// TestFull48BitRangeNoExpansion exercises extreme 48-bit boundaries. The sets
// stay represented by a handful of intervals; no 2^48 array is touched.
func TestFull48BitRangeNoExpansion(t *testing.T) {
	p := IdentityPerm(PageBits)
	full := IntervalSet{{0, MaxPage + 1}}
	image, err := p.Image(full)
	if err != nil {
		t.Fatalf("Image full space: %v", err)
	}
	if !EqualSet(image, full) {
		t.Fatalf("identity permutation changed the full page space")
	}
	if len(image) != 1 || image[0].End != MaxPage+1 {
		t.Fatalf("full space must remain one interval, got %v", image)
	}

	// Swapping the top and bottom routed bits over the whole space yields two
	// cylinders while preserving the total page count.
	swapped := IdentityPerm(PageBits)
	swapped.Map[0], swapped.Map[PageBits-1] = swapped.Map[PageBits-1], swapped.Map[0]
	swappedImage, err := swapped.Image(full)
	if err != nil {
		t.Fatalf("swapped Image: %v", err)
	}
	if swappedImage.PageCount() != full.PageCount() {
		t.Fatalf("swapped permutation changed page count")
	}
	// The whole domain is a fixed point of every bijection on that domain.
	if !EqualSet(swappedImage, full) {
		t.Fatalf("full-domain permutation must map onto the full domain, got %d intervals", len(swappedImage))
	}

	if _, err := p.Image(IntervalSet{{0, MaxPage + 2}}); err == nil {
		t.Fatalf("expected an error for a range escaping the page space")
	}

	// Shift overflow is reported, not wrapped.
	if _, err := (IntervalSet{{MaxPage, MaxPage + 1}}).Shift(1); err == nil {
		t.Fatalf("expected an overflow error from Shift")
	}
}

// TestVerifyDetectsTampering mutates an otherwise valid credential and checks
// that verification recomputing from the manifest rejects overstatement.
func TestVerifyDetectsTampering(t *testing.T) {
	owner := id("dev", 1, 1, "q")
	seg := Segment{LogBase: 0, PhysBase: 0x5000, Perm: IdentityPerm(2)}
	m := &Manifest{
		Windows: []Window{{Owner: owner, Segments: []Segment{seg}}},
		Leases: []Lease{{
			ID: "L", Owner: id("other", 1, 1, "q"), Perm: PermWrite,
			Pages: mustNormalize(t, []Interval{{0x5002, 0x5004}}),
		}},
	}
	cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: mustNormalize(t, []Interval{{0, 4}})})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	assertPages(t, cred.SafeLogical, []uint64{0, 1})

	fake := *cred
	fake.SafeLogical = mustNormalize(t, []Interval{{0, 4}})
	if err := Verify(m, &fake); err == nil {
		t.Fatalf("Verify accepted an overstated safe range")
	}

	fake = *cred
	fake.Gaps = nil
	if err := Verify(m, &fake); err == nil {
		t.Fatalf("Verify accepted a credential with hidden gaps")
	}

	fake = *cred
	fake.LeaseIDs = []string{"L", "ghost-lease"}
	if err := Verify(m, &fake); err == nil {
		t.Fatalf("Verify accepted fabricated evidence references")
	}

	if err := Verify(m, cred); err != nil {
		t.Fatalf("Verify rejected the genuine credential: %v", err)
	}
}

// TestValidationErrors covers malformed inputs and window containment rules.
func TestValidationErrors(t *testing.T) {
	if _, err := Normalize([]Interval{{5, 5}}); err == nil {
		t.Fatalf("empty interval must be rejected")
	}
	if _, err := Normalize([]Interval{{MaxPage, MaxPage + 2}}); err == nil {
		t.Fatalf("out-of-space interval must be rejected")
	}
	if err := (Perm{N: 3, Map: []uint{0, 0, 1}}).Validate(); err == nil {
		t.Fatalf("non-bijective permutation must be rejected")
	}
	if err := (Perm{N: 3, Map: []uint{0, 1, 3}}).Validate(); err == nil {
		t.Fatalf("out-of-range permutation entry must be rejected")
	}
	seg := Segment{LogBase: 1, PhysBase: 0, Perm: IdentityPerm(2)}
	if err := seg.Validate(); err == nil {
		t.Fatalf("misaligned segment must be rejected")
	}

	owner := id("dev", 1, 1, "q")
	goodSeg := Segment{LogBase: 0, PhysBase: 0x6000, Perm: IdentityPerm(2)}
	m := &Manifest{Windows: []Window{{Owner: owner, Segments: []Segment{goodSeg}}}}
	if _, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: mustNormalize(t, []Interval{{0, 8}})}); err == nil {
		t.Fatalf("request outside the window must be rejected")
	}
	if _, err := Issue(m, HandoverRequest{WindowOwner: id("missing", 1, 1, "q"), Requested: mustNormalize(t, []Interval{{0, 4}})}); err == nil {
		t.Fatalf("unknown window owner must be rejected")
	}
	if _, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: nil}); err == nil {
		t.Fatalf("empty request must be rejected")
	}
}

// TestMultiSegmentWindow checks a request spanning two segments with different
// permutations, including physical-image reconstruction back to logical gaps.
func TestMultiSegmentWindow(t *testing.T) {
	owner := id("dev", 1, 1, "q")
	reverse := Perm{N: 3, Map: []uint{2, 1, 0}}
	seg1 := Segment{LogBase: 0, PhysBase: 0x7000, Perm: IdentityPerm(3)}
	seg2 := Segment{LogBase: 8, PhysBase: 0x7100, Perm: reverse}
	m := &Manifest{
		Windows: []Window{{Owner: owner, Segments: []Segment{seg1, seg2}}},
		Leases: []Lease{{
			ID: "L", Owner: id("old", 1, 1, "q"), Perm: PermRead,
			// Block logical page 1 (identity segment) and local page 5 of the
			// reversed segment: logical 8+5 = 13.
			Pages: pageSet(0x7001, 0x7100+refApply(reverse, 5)),
		}},
	}
	cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: mustNormalize(t, []Interval{{0, 16}})})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	assertPages(t, cred.SafeLogical, []uint64{0, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 15})
	if len(cred.Gaps) != 1 {
		t.Fatalf("expected one read gap spanning both segments, got %+v", cred.Gaps)
	}
	assertPages(t, cred.Gaps[0].Logical, []uint64{1, 13})
	if err := Verify(m, cred); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
