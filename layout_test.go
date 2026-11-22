package layout

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func mustPlanner(t *testing.T, cfg Config) *Planner {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func bruteLayouts(t *testing.T, p *Planner) []Layout {
	t.Helper()
	var out []Layout
	starts := make([]int, p.n)
	var walk func(int)
	walk = func(i int) {
		if i == p.n {
			l := Layout{Starts: append([]int(nil), starts...)}
			if p.Validate(l) == nil {
				out = append(out, l)
			}
			return
		}
		for _, s := range p.domains[i] {
			starts[i] = s
			walk(i + 1)
		}
	}
	walk(0)
	sort.Slice(out, func(i, j int) bool { return lessLayout(out[i], out[j]) })
	return out
}

func lessLayout(a, b Layout) bool {
	for i := 0; i < len(a.Starts) && i < len(b.Starts); i++ {
		if a.Starts[i] != b.Starts[i] {
			return a.Starts[i] < b.Starts[i]
		}
	}
	return len(a.Starts) < len(b.Starts)
}

func TestExhaustiveRankUnrank(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{
			name: "alignment_and_reservation",
			cfg: Config{
				Pages:    5,
				Regions:  []Region{{Name: "a", Length: 2, Alignment: 1}, {Name: "b", Length: 1, Alignment: 2}, {Name: "c", Length: 1, Alignment: 1}},
				Reserved: []int{2},
			},
		},
		{
			name: "modular_constraints",
			cfg: Config{
				Pages: 5,
				Regions: []Region{
					{Name: "a", Length: 1, Alignment: 1},
					{Name: "b", Length: 2, Alignment: 2},
					{Name: "c", Length: 1, Alignment: 1},
				},
				Constraints: []Constraint{
					{A: 0, B: 1, Modulus: 3, Allowed: []uint64{0, 1}},
					{A: 1, B: 2, Modulus: 2, Allowed: []uint64{1}},
				},
			},
		},
		{
			name: "pinned_stable_and_lease",
			cfg: Config{
				Pages:      6,
				Generation: 2,
				Regions: []Region{
					{Name: "db", Length: 2, Alignment: 1, Stable: true, Generation: 1},
					{Name: "log", Length: 1, Alignment: 1, Pinned: true, Start: 0},
					{Name: "tmp", Length: 1, Alignment: 1},
					{Name: "x", Length: 2, Alignment: 2},
				},
				Stable: []StableMapping{
					{Name: "db", Generation: 1, Start: 2, Length: 2},
				},
				Leases: []Lease{
					{Name: "db", Generation: 1, Start: 2, Length: 2},
				},
			},
		},
		{
			name: "same_start_modulus",
			cfg: Config{
				Pages:   4,
				Regions: []Region{{Name: "a", Length: 1, Alignment: 1}, {Name: "b", Length: 1, Alignment: 1}},
				Constraints: []Constraint{
					{A: 0, B: 0, Modulus: 7, Allowed: []uint64{0}},
					{A: 0, B: 1, Modulus: 3, Allowed: []uint64{0, 2}},
				},
			},
		},
		{
			name: "constraints_with_fixed_prefix",
			cfg: Config{
				Pages: 5,
				Regions: []Region{
					{Name: "fixed", Length: 1, Alignment: 1, Pinned: true, Start: 0},
					{Name: "b", Length: 1, Alignment: 1},
					{Name: "c", Length: 1, Alignment: 1},
				},
				Constraints: []Constraint{
					{A: 0, B: 1, Modulus: 3, Allowed: []uint64{1}},
					{A: 1, B: 2, Modulus: 2, Allowed: []uint64{0}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustPlanner(t, tc.cfg)
			all := bruteLayouts(t, p)
			if got, want := p.Count(), big.NewInt(int64(len(all))); got.Cmp(want) != 0 {
				t.Fatalf("Count() = %v, exhaustive count = %v", got, want)
			}
			for i, want := range all {
				if err := p.Validate(want); err != nil {
					t.Fatalf("exhaustive layout %d is invalid: %v", i, err)
				}
				gotRank, err := p.Rank(want)
				if err != nil {
					t.Fatalf("Rank(%v): %v", want, err)
				}
				if gotRank.Cmp(big.NewInt(int64(i))) != 0 {
					t.Fatalf("Rank(%v) = %v, want %d", want, gotRank, i)
				}
				got, err := p.Unrank(big.NewInt(int64(i)))
				if err != nil {
					t.Fatalf("Unrank(%d): %v", i, err)
				}
				if !got.Equal(want) {
					t.Fatalf("Unrank(%d) = %v, want %v", i, got, want)
				}
			}
			if _, err := p.Unrank(big.NewInt(int64(len(all)))); !errors.Is(err, ErrRankOutOfRange) {
				t.Fatalf("Unrank(Count) error = %v, want ErrRankOutOfRange", err)
			}
		})
	}
}

func TestCountBeyond32Bits(t *testing.T) {
	cfg := Config{Pages: 48}
	for i := 0; i < MaxRegions; i++ {
		cfg.Regions = append(cfg.Regions, Region{
			Name:      fmt.Sprintf("r%d", i),
			Length:    1,
			Alignment: 1,
		})
	}
	p := mustPlanner(t, cfg)

	want := big.NewInt(1)
	for i := 0; i < MaxRegions; i++ {
		want.Mul(want, big.NewInt(int64(48-i)))
	}
	if want.BitLen() <= 32 {
		t.Fatalf("test setup: count %v is not beyond 32 bits", want)
	}
	got := p.Count()
	if got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want %v", got, want)
	}

	got.SetInt64(0)
	if again := p.Count(); again.Cmp(want) != 0 {
		t.Fatalf("Count() was mutated through its return value: %v", again)
	}
}

func TestAlignmentAndReservedPages(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:    8,
		Regions:  []Region{{Name: "a", Length: 3, Alignment: 2}},
		Reserved: []int{2},
	})
	if got, want := fmt.Sprint(p.domains[0]), "[4]"; got != want {
		t.Fatalf("domain = %s, want %s", got, want)
	}
	if got := p.Count(); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Count() = %v, want 1", got)
	}
}

func TestConstraintsModulo(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:   4,
		Regions: []Region{{Name: "a", Length: 1, Alignment: 1}, {Name: "b", Length: 1, Alignment: 1}},
		Constraints: []Constraint{
			{A: 0, B: 1, Modulus: 3, Allowed: []uint64{0}},
		},
	})
	all := bruteLayouts(t, p)
	want := []Layout{{Starts: []int{0, 3}}, {Starts: []int{3, 0}}}
	if len(all) != len(want) {
		t.Fatalf("layouts = %v, want %v", all, want)
	}
	for i := range want {
		if !all[i].Equal(want[i]) {
			t.Fatalf("layouts = %v, want %v", all, want)
		}
	}

	p = mustPlanner(t, Config{
		Pages:   4,
		Regions: []Region{{Name: "a", Length: 1, Alignment: 1}},
		Constraints: []Constraint{
			{A: 0, B: 0, Modulus: 5, Allowed: []uint64{1}},
		},
	})
	if got := p.Count(); got.Sign() != 0 {
		t.Fatalf("Count() = %v, want 0 for impossible self constraint", got)
	}
}

func TestSymmetricRegionsWithDifferentNames(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages: 3,
		Regions: []Region{
			{Name: "left", Length: 1, Alignment: 1},
			{Name: "right", Length: 1, Alignment: 1},
		},
	})
	if got, want := p.Count(), big.NewInt(6); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want %v (ordered by region number)", got, want)
	}
	r1, err := p.Rank(Layout{Starts: []int{0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.Rank(Layout{Starts: []int{1, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Cmp(r2) == 0 {
		t.Fatalf("symmetric layouts share rank %v", r1)
	}
	for i := int64(0); i < 6; i++ {
		l, err := p.Unrank(big.NewInt(i))
		if err != nil {
			t.Fatal(err)
		}
		if l.Starts[0] == l.Starts[1] {
			t.Fatalf("Unrank(%d) overlaps: %v", i, l)
		}
	}
}

func TestRevokeOldReadersChangesAvailableSpace(t *testing.T) {
	base := Config{
		Pages:      5,
		Generation: 2,
		Regions:    []Region{{Name: "vm", Length: 2, Alignment: 1}},
	}
	withLease := base
	withLease.Leases = []Lease{{Name: "old", Generation: 1, Start: 1, Length: 2}}

	pLease := mustPlanner(t, withLease)
	pFree := mustPlanner(t, base)
	if got := pLease.Count(); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("count with lease = %v, want 1", got)
	}
	if got := pFree.Count(); got.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("count after revoking readers = %v, want 4", got)
	}
}

func TestStableReuseRequiresSameNameAndGeneration(t *testing.T) {
	base := Config{
		Pages:      6,
		Generation: 2,
		Regions:    []Region{{Name: "db", Length: 2, Alignment: 1, Stable: true, Generation: 7}},
		Stable:     []StableMapping{{Name: "db", Generation: 7, Start: 2, Length: 2}},
		Leases:     []Lease{{Name: "db", Generation: 7, Start: 2, Length: 2}},
	}
	p := mustPlanner(t, base)
	if got, want := p.Count(), big.NewInt(1); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want %v", got, want)
	}

	wrongGeneration := base
	wrongGeneration.Regions = append([]Region(nil), base.Regions...)
	wrongGeneration.Regions[0].Generation = 8
	if _, err := New(wrongGeneration); err == nil {
		t.Fatal("New accepted stable reuse with wrong generation")
	}

	wrongName := base
	wrongName.Regions = append([]Region(nil), base.Regions...)
	wrongName.Regions[0].Name = "other"
	if _, err := New(wrongName); err == nil {
		t.Fatal("New accepted stable reuse with wrong name")
	}
}

func TestLeaseBlocksOtherObjects(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:      5,
		Generation: 2,
		Regions:    []Region{{Name: "vm", Length: 1, Alignment: 1}},
		Leases:     []Lease{{Name: "old", Generation: 1, Start: 1, Length: 2}},
	})
	if got, want := fmt.Sprint(p.domains[0]), "[0 3 4]"; got != want {
		t.Fatalf("domain = %s, want %s", got, want)
	}
}

func TestEmptyConfiguration(t *testing.T) {
	p := mustPlanner(t, Config{Pages: 0})
	if got, want := p.Count(), big.NewInt(1); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want 1", got)
	}
	l, err := p.Unrank(big.NewInt(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Starts) != 0 {
		t.Fatalf("layout = %v, want empty", l)
	}
	if _, err := p.Unrank(big.NewInt(1)); !errors.Is(err, ErrRankOutOfRange) {
		t.Fatalf("Unrank(1) error = %v", err)
	}
	src := NewSliceSource(nil)
	s, err := p.NewSampler(src)
	if err != nil {
		t.Fatal(err)
	}
	sampled, err := s.Draw()
	if err != nil {
		t.Fatal(err)
	}
	if len(sampled.Starts) != 0 || src.Position() != 0 {
		t.Fatalf("empty sample = %v, consumed %d bits", sampled, src.Position())
	}
}

func TestNoLayouts(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:   2,
		Regions: []Region{{Name: "too-big", Length: 3, Alignment: 1}},
	})
	if got := p.Count(); got.Sign() != 0 {
		t.Fatalf("Count() = %v, want 0", got)
	}
	if _, err := p.Unrank(big.NewInt(0)); !errors.Is(err, ErrNoLayouts) {
		t.Fatalf("Unrank error = %v, want ErrNoLayouts", err)
	}
	if _, err := p.NewSampler(NewSliceSource(nil)); !errors.Is(err, ErrNoLayouts) {
		t.Fatalf("NewSampler error = %v, want ErrNoLayouts", err)
	}
}

func TestConfigIsCopied(t *testing.T) {
	cfg := Config{
		Pages:   4,
		Regions: []Region{{Name: "a", Length: 1, Alignment: 1}},
	}
	p := mustPlanner(t, cfg)
	cfg.Regions[0].Name = "changed"
	if p.cfg.Regions[0].Name != "a" {
		t.Fatalf("planner retained caller's mutated config: %v", p.cfg.Regions)
	}
	got := p.Config()
	got.Regions[0].Name = "changed-again"
	if p.cfg.Regions[0].Name != "a" {
		t.Fatalf("Config() exposed mutable internal state")
	}
}

type randomBitSource struct {
	r *rand.Rand
}

func (s *randomBitSource) NextBit() (uint8, error) {
	return uint8(s.r.Intn(2)), nil
}

func TestSampleUniformitySmall(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages: 4,
		Regions: []Region{
			{Name: "left", Length: 1, Alignment: 1},
			{Name: "right", Length: 1, Alignment: 1},
		},
	})
	const samples = 6000
	counts := make(map[string]int)
	rngSrc := &randomBitSource{r: rand.New(rand.NewSource(1))}
	for i := 0; i < samples; i++ {
		s, err := p.NewSampler(rngSrc)
		if err != nil {
			t.Fatal(err)
		}
		l, err := s.Draw()
		if err != nil {
			t.Fatal(err)
		}
		counts[fmt.Sprint(l.Starts)]++
	}
	if len(counts) != 12 {
		t.Fatalf("sampled %d distinct layouts, want 12: %v", len(counts), counts)
	}
	expected := float64(samples) / 12
	chi := 0.0
	for _, c := range counts {
		d := float64(c) - expected
		chi += d * d / expected
	}
	if chi > 45 {
		t.Fatalf("chi-square = %.2f, distribution is suspicious: %v", chi, counts)
	}
}

func TestRejectionSamplingNonPowerOfTwo(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages: 4,
		Regions: []Region{
			{Name: "a", Length: 1, Alignment: 1},
			{Name: "b", Length: 1, Alignment: 1},
		},
	})
	if got, want := p.Count(), big.NewInt(12); got.Cmp(want) != 0 {
		t.Fatalf("Count() = %v, want 12", got)
	}

	// 1111 is rejected (15 >= 12); 0011 is accepted as rank 3.
	src := NewSliceSource([]byte{0xF3})
	got, err := p.Sample(src)
	if err != nil {
		t.Fatal(err)
	}
	want, err := p.Unrank(big.NewInt(3))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("Sample = %v, want %v", got, want)
	}
	if src.Position() != 8 {
		t.Fatalf("consumed %d bits, want 8", src.Position())
	}

	// 1100 is rejected (12); 0000 is accepted as rank 0.
	src = NewSliceSource([]byte{0xC0})
	got, err = p.Sample(src)
	if err != nil {
		t.Fatal(err)
	}
	want, _ = p.Unrank(big.NewInt(0))
	if !got.Equal(want) {
		t.Fatalf("Sample = %v, want %v", got, want)
	}
	if src.Position() != 8 {
		t.Fatalf("consumed %d bits, want 8", src.Position())
	}
}

type limitSource struct {
	src   *SliceSource
	limit int
}

func (s *limitSource) NextBit() (uint8, error) {
	if s.limit == 0 {
		return 0, ErrNeedMoreBits
	}
	s.limit--
	return s.src.NextBit()
}

func (s *limitSource) Position() uint64 { return s.src.Position() }

func TestCheckpointResumeAtEveryBit(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages: 4,
		Regions: []Region{
			{Name: "a", Length: 1, Alignment: 1},
			{Name: "b", Length: 1, Alignment: 1},
		},
	})
	// The first attempt is 1111 (rejected), then 0000 (rank 0), so pause
	// points include the rejected-attempt state as well as the accepted one.
	data := []byte{0xFF, 0x00, 0xA5, 0x3C, 0xC3, 0x0F, 0xF0, 0x99, 0x66}

	fullSrc := NewSliceSource(data)
	fullSampler, err := p.NewSampler(fullSrc)
	if err != nil {
		t.Fatal(err)
	}
	fullLayout, err := fullSampler.Draw()
	if err != nil {
		t.Fatal(err)
	}
	finalPos := fullSrc.Position()
	if finalPos < 4 {
		t.Fatalf("test needs a non-trivial sample, final position is %d", finalPos)
	}

	for k := uint64(0); k < finalPos; k++ {
		limited := &limitSource{src: NewSliceSourceAt(data, 0), limit: int(k)}
		s, err := p.NewSampler(limited)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Draw(); !errors.Is(err, ErrNeedMoreBits) {
			t.Fatalf("pause at bit %d: Draw error = %v, want ErrNeedMoreBits", k, err)
		}
		cp, err := s.Checkpoint()
		if err != nil {
			t.Fatal(err)
		}
		if cp.BitPosition() != k {
			t.Fatalf("checkpoint position = %d, want %d", cp.BitPosition(), k)
		}
		encoded, err := cp.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var decoded Checkpoint
		if err := decoded.UnmarshalBinary(encoded); err != nil {
			t.Fatal(err)
		}

		resumeSrc := NewSliceSourceAt(data, decoded.BitPosition())
		rs, err := p.Resume(decoded, resumeSrc)
		if err != nil {
			t.Fatalf("pause at bit %d: Resume: %v", k, err)
		}
		got, err := rs.Draw()
		if err != nil {
			t.Fatalf("pause at bit %d: resumed Draw: %v", k, err)
		}
		if !got.Equal(fullLayout) {
			t.Fatalf("pause at bit %d: got %v, want %v", k, got, fullLayout)
		}
		if resumeSrc.Position() != finalPos {
			t.Fatalf("pause at bit %d: resumed position %d, want %d", k, resumeSrc.Position(), finalPos)
		}

		wrongPos := NewSliceSourceAt(data, decoded.BitPosition()+1)
		if _, err := p.Resume(decoded, wrongPos); !errors.Is(err, ErrBitPosition) {
			t.Fatalf("pause at bit %d: wrong-position Resume error = %v", k, err)
		}
	}

	doneCP, err := fullSampler.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	afterSrc := NewSliceSourceAt(data, finalPos)
	rs, err := p.Resume(doneCP, afterSrc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rs.Draw()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(fullLayout) {
		t.Fatalf("done checkpoint resumed to %v, want %v", got, fullLayout)
	}
	if afterSrc.Position() != finalPos {
		t.Fatalf("done checkpoint consumed %d extra bits", afterSrc.Position()-finalPos)
	}
}

func TestCheckpointInvalidation(t *testing.T) {
	base := Config{
		Pages:      4,
		Generation: 3,
		Regions: []Region{
			{Name: "a", Length: 1, Alignment: 1},
			{Name: "b", Length: 1, Alignment: 1},
		},
	}
	p := mustPlanner(t, base)
	limited := &limitSource{src: NewSliceSourceAt([]byte{0xAA, 0x55}, 0), limit: 2}
	s, err := p.NewSampler(limited)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Draw(); !errors.Is(err, ErrNeedMoreBits) {
		t.Fatalf("Draw error = %v", err)
	}
	cp, err := s.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	withConstraint := base
	withConstraint.Constraints = []Constraint{{A: 0, B: 1, Modulus: 2, Allowed: []uint64{0}}}
	pConstraint := mustPlanner(t, withConstraint)
	if _, err := pConstraint.Resume(cp, NewSliceSourceAt([]byte{0xAA, 0x55}, 2)); !errors.Is(err, ErrStaleCheckpoint) {
		t.Fatalf("changed constraint Resume error = %v, want ErrStaleCheckpoint", err)
	}

	leaseGen1 := base
	leaseGen1.Leases = []Lease{{Name: "old", Generation: 1, Start: 3, Length: 1}}
	p1 := mustPlanner(t, leaseGen1)
	s1, err := p1.NewSampler(&limitSource{src: NewSliceSourceAt([]byte{0xAA, 0x55}, 0), limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Draw(); !errors.Is(err, ErrNeedMoreBits) {
		t.Fatal(err)
	}
	cp1, err := s1.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	leaseGen2 := leaseGen1
	leaseGen2.Leases = []Lease{{Name: "old", Generation: 2, Start: 3, Length: 1}}
	p2 := mustPlanner(t, leaseGen2)
	if _, err := p2.Resume(cp1, NewSliceSourceAt([]byte{0xAA, 0x55}, 2)); !errors.Is(err, ErrStaleCheckpoint) {
		t.Fatalf("changed lease generation Resume error = %v, want ErrStaleCheckpoint", err)
	}
}

func TestConstraintDirection(t *testing.T) {
	cfg := Config{
		Pages:   4,
		Regions: []Region{{Name: "a", Length: 1, Alignment: 1}, {Name: "b", Length: 1, Alignment: 1}},
		Constraints: []Constraint{
			{A: 0, B: 1, Modulus: 4, Allowed: []uint64{1}},
		},
	}
	p := mustPlanner(t, cfg)
	all := bruteLayouts(t, p)
	for _, l := range all {
		if diff := modDiff(l.Starts[0], l.Starts[1], 4); diff != 1 {
			t.Fatalf("layout %v has (a-b) mod 4 = %d, want 1", l, diff)
		}
	}
	if len(all) != 4 {
		t.Fatalf("got %d layouts, want 4: %v", len(all), all)
	}
}

func TestConstraintSetCanContainLargeModulus(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:   4,
		Regions: []Region{{Name: "a", Length: 1, Alignment: 1}, {Name: "b", Length: 1, Alignment: 1}},
		Constraints: []Constraint{
			{A: 0, B: 1, Modulus: 1<<63 + 5, Allowed: []uint64{2, 1<<63 + 1}},
		},
	})
	all := bruteLayouts(t, p)
	if len(all) == 0 {
		t.Fatal("expected some layouts for large modulus")
	}
	for _, l := range all {
		diff := modDiff(l.Starts[0], l.Starts[1], 1<<63+5)
		if diff != 2 && diff != 1<<63+1 {
			t.Fatalf("layout %v has unexpected residue %d", l, diff)
		}
	}
}

func TestStableLeaseDoesNotAllowOtherObjects(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:      6,
		Generation: 2,
		Regions: []Region{
			{Name: "db", Length: 2, Alignment: 1, Stable: true, Generation: 1},
			{Name: "tmp", Length: 1, Alignment: 1},
		},
		Stable: []StableMapping{{Name: "db", Generation: 1, Start: 2, Length: 2}},
		Leases: []Lease{{Name: "db", Generation: 1, Start: 2, Length: 2}},
	})
	if got, want := fmt.Sprint(p.domains[1]), "[0 1 4 5]"; got != want {
		t.Fatalf("tmp domain = %s, want %s", got, want)
	}
}

func TestExplicitPageLease(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:      5,
		Generation: 2,
		Regions:    []Region{{Name: "vm", Length: 1, Alignment: 1}},
		Leases:     []Lease{{Name: "old", Generation: 1, Pages: []int{1, 3}}},
	})
	if got, want := fmt.Sprint(p.domains[0]), "[0 2 4]"; got != want {
		t.Fatalf("domain = %s, want %s", got, want)
	}

	// A stable region with the same name/generation may reuse a span
	// contained in the old-reader page set; other pages in the set stay
	// blocked for everything else.
	p = mustPlanner(t, Config{
		Pages:      5,
		Generation: 2,
		Regions: []Region{
			{Name: "db", Length: 1, Alignment: 1, Stable: true, Generation: 1},
			{Name: "tmp", Length: 1, Alignment: 1},
		},
		Stable: []StableMapping{{Name: "db", Generation: 1, Start: 3, Length: 1}},
		Leases: []Lease{{Name: "db", Generation: 1, Pages: []int{1, 3}}},
	})
	if got, want := fmt.Sprint(p.domains[1]), "[0 2 4]"; got != want {
		t.Fatalf("tmp domain = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(p.domains[0]), "[3]"; got != want {
		t.Fatalf("db domain = %s, want %s", got, want)
	}
}

func TestAnonymousOldReaderPages(t *testing.T) {
	p := mustPlanner(t, Config{
		Pages:          5,
		Generation:     2,
		Regions:        []Region{{Name: "vm", Length: 1, Alignment: 1}},
		OldReaderPages: []int{1, 3, 3},
	})
	if got, want := fmt.Sprint(p.domains[0]), "[0 2 4]"; got != want {
		t.Fatalf("domain = %s, want %s", got, want)
	}
}

func TestLayoutStringAndClone(t *testing.T) {
	l := Layout{Starts: []int{1, 2}}
	c := l.Clone()
	c.Starts[0] = 9
	if l.Starts[0] != 1 {
		t.Fatal("Clone shares storage")
	}
	if got, want := l.String(), "[1 2]"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if !strings.Contains(l.String(), "1") {
		t.Fatal("String() lost content")
	}
}

func TestRandomSmallAgainstBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for caseNo := 0; caseNo < 200; caseNo++ {
		pages := 3 + rng.Intn(4)
		n := 1 + rng.Intn(4)
		cfg := Config{Pages: pages}
		for i := 0; i < n; i++ {
			cfg.Regions = append(cfg.Regions, Region{
				Name:      fmt.Sprintf("r%d", i),
				Length:    1 + rng.Intn(2),
				Alignment: 1 << uint(rng.Intn(2)),
			})
		}
		for page := 0; page < pages; page++ {
			if rng.Intn(5) == 0 {
				cfg.Reserved = append(cfg.Reserved, page)
			}
		}
		for k := 0; k < rng.Intn(4); k++ {
			a := rng.Intn(n)
			b := rng.Intn(n)
			modulus := uint64(1 + rng.Intn(6))
			var allowed []uint64
			for r := uint64(0); r < modulus; r++ {
				if rng.Intn(2) == 0 {
					allowed = append(allowed, r)
				}
			}
			cfg.Constraints = append(cfg.Constraints, Constraint{A: a, B: b, Modulus: modulus, Allowed: allowed})
		}

		p := mustPlanner(t, cfg)
		all := bruteLayouts(t, p)
		if got, want := p.Count(), big.NewInt(int64(len(all))); got.Cmp(want) != 0 {
			t.Fatalf("case %d: Count() = %v, brute = %v, cfg = %+v", caseNo, got, want, cfg)
		}
		for i, l := range all {
			rank, err := p.Rank(l)
			if err != nil {
				t.Fatalf("case %d: Rank(%v): %v", caseNo, l, err)
			}
			if rank.Cmp(big.NewInt(int64(i))) != 0 {
				t.Fatalf("case %d: Rank(%v) = %v, want %d", caseNo, l, rank, i)
			}
			got, err := p.Unrank(big.NewInt(int64(i)))
			if err != nil {
				t.Fatalf("case %d: Unrank(%d): %v", caseNo, i, err)
			}
			if !got.Equal(l) {
				t.Fatalf("case %d: Unrank(%d) = %v, want %v", caseNo, i, got, l)
			}
		}
	}
}
