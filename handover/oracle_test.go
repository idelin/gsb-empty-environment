package handover

import (
	"math/rand"
	"testing"
)

// TestPermutationOracle fuzzes random permutations over small bit fields and
// compares the structural Image against a page-by-page reference, including
// arbitrary sub-intervals and shifted segments.
func TestPermutationOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261120))
	for iter := 0; iter < 200; iter++ {
		n := uint(1 + rng.Intn(8))
		p := randomPerm(rng, n)
		domain := uint64(1) << n
		start := rng.Uint64() % domain
		end := start + 1 + rng.Uint64()%(domain-start)
		in := mustNormalize(t, []Interval{{start, end}})

		got, err := p.Image(in)
		if err != nil {
			t.Fatalf("Image failed: %v", err)
		}
		want := refImage(t, p, in)
		if !samePageSet(t, got, want) {
			t.Fatalf("perm %v image mismatch on [%d,%d):\n got %v\nwant %v", p.Map, start, end, got, want)
		}
		if got.PageCount() != in.PageCount() {
			t.Fatalf("permutation changed page count: %d -> %d", in.PageCount(), got.PageCount())
		}

		back, err := p.Inverse().Image(got)
		if err != nil {
			t.Fatalf("inverse Image failed: %v", err)
		}
		if !EqualSet(back, in) {
			t.Fatalf("inverse permutation did not recover the input")
		}

		space := uint64(1) << PageBits
		base := (rng.Uint64() % (space / domain)) * domain
		seg := Segment{LogBase: 0, PhysBase: base, Perm: p}
		mapped, err := segmentLogicalToPhysical(seg, in)
		if err != nil {
			t.Fatalf("segment mapping failed: %v", err)
		}
		var wantMapped []uint64
		for _, page := range toPages(t, in) {
			wantMapped = append(wantMapped, base+refApply(p, page))
		}
		if !samePageSet(t, mapped, fromPages(wantMapped)) {
			t.Fatalf("perm %v segment mapping mismatch\n got %v\nwant %v", p.Map, mapped, wantMapped)
		}
	}
}

// samePageSet compares two interval sets by their page contents rather than
// by interval layout, allowing independent normalization choices.
func samePageSet(t *testing.T, a, b IntervalSet) bool {
	t.Helper()
	pa, pb := toPages(t, a), toPages(t, b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return false
		}
	}
	return true
}

func randomSubsetPages(rng *rand.Rand, base, domain uint64) IntervalSet {
	var pages []uint64
	for p := uint64(0); p < domain; p++ {
		if rng.Intn(2) == 0 {
			pages = append(pages, base+p)
		}
	}
	if len(pages) == 0 {
		pages = []uint64{base}
	}
	return fromPages(pages)
}

// TestRandomSafetyOracle builds random small manifests and confirms the
// credential's safe/gap split matches an exhaustive page-by-page evaluation.
func TestRandomSafetyOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	owner := id("host-dev", 1, 1, "winq")
	for iter := 0; iter < 100; iter++ {
		n := uint(2 + rng.Intn(4))
		domain := uint64(1) << n
		base := uint64(0x4000)
		seg := Segment{LogBase: 0, PhysBase: base, Perm: randomPerm(rng, n)}
		m := &Manifest{Windows: []Window{{Owner: owner, Segments: []Segment{seg}}}}

		identities := []Identity{
			id("old-0", 1, 1, "q"),
			id("old-1", 1, 2, "q"),
			id("old-0", 2, 1, "q"),
		}
		perms := []PermBits{PermRead, PermWrite, PermRead | PermWrite}
		for li, who := range identities {
			m.Leases = append(m.Leases, Lease{
				ID: "L" + itoa(uint64(li)), Owner: who,
				Perm: perms[rng.Intn(3)], Pages: randomSubsetPages(rng, base, domain),
			})
		}
		for ci := 0; ci < 4; ci++ {
			who := identities[rng.Intn(len(identities))]
			if rng.Intn(2) == 0 {
				who = id("stranger", 9, 9, "q")
			}
			m.Confirmations = append(m.Confirmations, Confirmation{
				ID: "C" + itoa(uint64(ci)), Owner: who,
				Perm: perms[rng.Intn(3)], Pages: randomSubsetPages(rng, base, domain),
			})
		}

		requested := mustNormalize(t, []Interval{{0, domain}})
		cred, err := Issue(m, HandoverRequest{WindowOwner: owner, Requested: requested})
		if err != nil {
			t.Fatalf("Issue failed: %v", err)
		}
		physicalAll := mustNormalize(t, []Interval{{base, base + domain}})
		wantSafe := refSafety(t, m, physicalAll)
		if !EqualSet(cred.SafePhysical, wantSafe) {
			t.Fatalf("iter %d safe set mismatch:\n got %v\nwant %v", iter, cred.SafePhysical, wantSafe)
		}
		var gapUnion IntervalSet
		for _, gap := range cred.Gaps {
			gapUnion = Union(gapUnion, gap.Physical)
		}
		if !EqualSet(Union(cred.SafePhysical, gapUnion), physicalAll) {
			t.Fatalf("iter %d: safe pages and gaps do not partition the request", iter)
		}
		if overlap := Intersect(cred.SafePhysical, gapUnion); !overlap.IsEmpty() {
			t.Fatalf("iter %d: safe pages and gaps overlap at %v", iter, overlap)
		}
		if err := Verify(m, cred); err != nil {
			t.Fatalf("Verify failed: %v", err)
		}
	}
}
