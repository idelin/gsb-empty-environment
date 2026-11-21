package admission

import (
	"errors"
	"sort"
	"testing"
)

func TestSmallDomainExhaustiveProofAndWrite(t *testing.T) {
	g := smallGraph()
	snap, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	root := mustVolume(t, g, "root")

	full, err := snap.Proof("root", 0, root.Length)
	if err != nil {
		t.Fatalf("full proof: %v", err)
	}
	checkProofShape(t, full, root.Length)

	covered := make(map[uint64]int)
	for _, claim := range full.Claims {
		checkClaimPath(t, claim)
		for _, v := range expandSmallClass(t, claim.Virtual) {
			covered[v]++
			wantPhys, wantPerm, err := resolveSector(g, "root", v)
			if err != nil {
				t.Fatalf("resolve sector %d: %v", v, err)
			}
			present, physCount := classContains(claim.Physical, wantPhys)
			if !present {
				t.Fatalf("virtual %d maps to physical %d, absent from claim %+v", v, wantPhys, claim.Physical)
			}
			if physCount != 1 {
				t.Fatalf("physical %d repeated in class %+v", wantPhys, claim.Physical)
			}
			if claim.Access != wantPerm {
				t.Fatalf("virtual %d access = %s, want %s", v, claim.Access, wantPerm)
			}
		}
	}
	if len(covered) != int(root.Length) {
		t.Fatalf("proof covers %d virtual sectors, want %d", len(covered), root.Length)
	}
	for v, n := range covered {
		if n != 1 {
			t.Fatalf("virtual sector %d is covered %d times", v, n)
		}
		wantPhys, wantPerm, err := resolveSector(g, "root", v)
		if err != nil {
			t.Fatal(err)
		}
		one, err := snap.Proof("root", v, 1)
		if err != nil {
			t.Fatalf("sector proof %d: %v", v, err)
		}
		if len(one.Claims) != 1 {
			t.Fatalf("sector %d proof has %d claims", v, len(one.Claims))
		}
		if got, n := classContains(one.Claims[0].Physical, wantPhys); !got || n != 1 {
			t.Fatalf("sector %d physical image %+v does not contain %d exactly once", v, one.Claims[0].Physical, wantPhys)
		}
		if one.Claims[0].Access != wantPerm {
			t.Fatalf("sector %d access = %s, want %s", v, one.Claims[0].Access, wantPerm)
		}
		_, writable, err := snap.CheckWrite("root", v, 1)
		if err != nil {
			t.Fatalf("write sector %d: %v", v, err)
		}
		if writable != wantPerm.AllowsWrite() {
			t.Fatalf("sector %d writable = %v, want %v (%s)", v, writable, wantPerm.AllowsWrite(), wantPerm)
		}
	}

	if _, writable, err := snap.CheckWrite("root", 0, 8); err != nil || !writable {
		t.Fatalf("writable prefix: writable=%v err=%v", writable, err)
	}
	if _, writable, err := snap.CheckWrite("root", 8, 4); err != nil || writable {
		t.Fatalf("read-only alias: writable=%v err=%v", writable, err)
	}
	if _, writable, err := snap.CheckWrite("root", 7, 2); err != nil || writable {
		t.Fatalf("crossing write: writable=%v err=%v", writable, err)
	}
}

func TestUnreachableSegmentsAreIgnored(t *testing.T) {
	g := smallGraph()
	g.Volumes = append(g.Volumes, Volume{
		Name:   "unreachable_bad",
		Length: 8,
		Segments: []Segment{
			Linear(0, 8, PermReadWrite, "missing", 0),
			Linear(0, 8, PermReadWrite, "unreachable_bad", 0),
		},
	})
	if _, err := Compile(g); err != nil {
		t.Fatalf("unreachable malformed segment was analysed: %v", err)
	}
}

func TestLargeStripeTailAndLastSector(t *testing.T) {
	const (
		w     = uint64(1 << 20)
		max   = MaxSectors
		left  = uint64(1 << 47)
		right = (1 << 47) - 1
	)
	g := Graph{
		Roots: []string{"root"},
		Physical: []PhysicalRange{
			{Start: 0, Length: left, Permission: PermReadWrite},
			{Start: left, Length: right, Permission: PermReadWrite},
		},
		Volumes: []Volume{
			{Name: "L", Length: left, Segments: []Segment{Physical(0, left, PermReadWrite, 0)}},
			{Name: "R", Length: right, Segments: []Segment{Physical(0, right, PermReadWrite, left)}},
			{Name: "root", Length: max - 1, Segments: []Segment{
				Stripe(0, max-1, PermReadWrite, w, "L", 0, "R", 0),
			}},
		},
	}
	snap, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile large stripe: %v", err)
	}
	p, err := snap.Proof("root", 0, max-1)
	if err != nil {
		t.Fatalf("large proof: %v", err)
	}
	if len(p.Claims) == 0 || len(p.Claims) > 4 {
		t.Fatalf("expected a compact proof, got %d claims", len(p.Claims))
	}
	var total uint64
	for _, c := range p.Claims {
		n, ok := c.Virtual.Size()
		if !ok {
			t.Fatalf("claim size overflow")
		}
		total += n
	}
	if total != max-1 {
		t.Fatalf("proof covers %d sectors, want %d", total, max-1)
	}
	last, err := snap.Proof("root", max-2, 1)
	if err != nil {
		t.Fatalf("last sector proof: %v", err)
	}
	if !classContainsExactly(last.Claims[0].Physical, max-2) {
		t.Fatalf("last virtual sector physical = %+v, want %d", last.Claims[0].Physical, max-2)
	}
	if _, ok, err := snap.CheckWrite("root", max-2, 1); err != nil || !ok {
		t.Fatalf("last write: ok=%v err=%v", ok, err)
	}
	head, err := snap.Proof("root", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !classContainsExactly(head.Claims[0].Physical, 0) {
		t.Fatalf("head physical = %+v", head.Claims[0].Physical)
	}
}

func TestStripeHeadsAndTailsAcrossWidths(t *testing.T) {
	for _, width := range []uint64{1, 2, 4, 8} {
		for length := uint64(1); length <= 13; length++ {
			name := "w" + itoa(width) + "l" + itoa(length)
			t.Run(name, func(t *testing.T) {
				g := Graph{
					Roots: []string{"v"},
					Physical: []PhysicalRange{
						{Start: 1000, Length: 16, Permission: PermReadWrite},
						{Start: 2000, Length: 16, Permission: PermReadWrite},
					},
					Volumes: []Volume{
						{Name: "a", Length: 16, Segments: []Segment{Physical(0, 16, PermReadWrite, 1000)}},
						{Name: "b", Length: 16, Segments: []Segment{Physical(0, 16, PermReadWrite, 2000)}},
						{Name: "v", Length: length, Segments: []Segment{
							Stripe(0, length, PermReadWrite, width, "a", 0, "b", 0),
						}},
					},
				}
				snap, err := Compile(g)
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				proof, err := snap.Proof("v", 0, length)
				if err != nil {
					t.Fatalf("Proof: %v", err)
				}
				checkProofShape(t, proof, length)
				for v := uint64(0); v < length; v++ {
					wantPhys, wantPerm, err := resolveSector(g, "v", v)
					if err != nil {
						t.Fatal(err)
					}
					one, err := snap.Proof("v", v, 1)
					if err != nil {
						t.Fatal(err)
					}
					if len(one.Claims) != 1 ||
						!classContainsExactly(one.Claims[0].Physical, wantPhys) ||
						one.Claims[0].Access != wantPerm {
						t.Fatalf("sector %d: claims=%+v want phys=%d perm=%s",
							v, one.Claims, wantPhys, wantPerm)
					}
				}
			})
		}
	}
}

func TestNestedStripesSmallExhaustive(t *testing.T) {
	g := Graph{
		Roots: []string{"root"},
		Physical: []PhysicalRange{
			{Start: 3000, Length: 16, Permission: PermReadWrite},
			{Start: 4000, Length: 16, Permission: PermReadWrite},
		},
		Volumes: []Volume{
			{Name: "a", Length: 16, Segments: []Segment{Physical(0, 16, PermReadWrite, 3000)}},
			{Name: "b", Length: 16, Segments: []Segment{Physical(0, 16, PermReadWrite, 4000)}},
			{Name: "midL", Length: 16, Segments: []Segment{
				Stripe(0, 16, PermReadWrite, 2, "a", 0, "b", 0),
			}},
			{Name: "midR", Length: 16, Segments: []Segment{
				Stripe(0, 16, PermReadWrite, 1, "a", 8, "b", 8),
			}},
			{Name: "root", Length: 32, Segments: []Segment{
				Stripe(0, 32, PermReadWrite, 4, "midL", 0, "midR", 0),
			}},
		},
	}
	snap, err := Compile(g)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	proof, err := snap.Proof("root", 0, 32)
	if err != nil {
		t.Fatalf("Proof: %v", err)
	}
	checkProofShape(t, proof, 32)
	for v := uint64(0); v < 32; v++ {
		wantPhys, wantPerm, err := resolveSector(g, "root", v)
		if err != nil {
			t.Fatalf("resolve %d: %v", v, err)
		}
		one, err := snap.Proof("root", v, 1)
		if err != nil {
			t.Fatalf("proof %d: %v", v, err)
		}
		if len(one.Claims) != 1 ||
			!classContainsExactly(one.Claims[0].Physical, wantPhys) ||
			one.Claims[0].Access != wantPerm {
			t.Fatalf("sector %d: claims=%+v want phys=%d perm=%s", v, one.Claims, wantPhys, wantPerm)
		}
	}
	for start := uint64(0); start < 32; start++ {
		for length := uint64(1); start+length <= 32; length++ {
			ranged, err := snap.Proof("root", start, length)
			if err != nil {
				t.Fatalf("Proof(%d,%d): %v", start, length, err)
			}
			checkProofShape(t, ranged, length)
			for _, claim := range ranged.Claims {
				for _, v := range expandSmallClass(t, claim.Virtual) {
					wantPhys, _, err := resolveSector(g, "root", v)
					if err != nil {
						t.Fatal(err)
					}
					if !classContainsExactly(claim.Physical, wantPhys) {
						t.Fatalf("range [%d,%d): virtual %d physical class %+v misses %d",
							start, start+length, v, claim.Physical, wantPhys)
					}
				}
			}
		}
	}
}

func TestOverflowAndLayerChecks(t *testing.T) {
	tests := []struct {
		name string
		g    Graph
		want error
	}{
		{
			name: "volume length over 2^48",
			g: Graph{Roots: []string{"v"}, Volumes: []Volume{{
				Name: "v", Length: MaxSectors + 1,
			}}},
			want: ErrInvalid,
		},
		{
			name: "physical end overflow",
			g: Graph{
				Roots:    []string{"v"},
				Physical: []PhysicalRange{{Start: 0, Length: 8, Permission: PermReadWrite}},
				Volumes: []Volume{{Name: "v", Length: 2, Segments: []Segment{
					Physical(0, 2, PermReadWrite, MaxSectors-1),
				}}},
			},
			want: ErrOverflow,
		},
		{
			name: "linear child end overflow",
			g: Graph{
				Roots: []string{"v"},
				Volumes: []Volume{
					{Name: "c", Length: 1},
					{Name: "v", Length: 2, Segments: []Segment{Linear(0, 2, PermReadWrite, "c", 0)}},
				},
			},
			want: ErrOverflow,
		},
		{
			name: "non-power stripe",
			g: Graph{
				Roots: []string{"v"},
				Volumes: []Volume{
					{Name: "a", Length: 8}, {Name: "b", Length: 8},
					{Name: "v", Length: 8, Segments: []Segment{
						Stripe(0, 8, PermReadWrite, 3, "a", 0, "b", 0),
					}},
				},
			},
			want: ErrInvalid,
		},
		{
			name: "stripe child capacity",
			g: Graph{
				Roots: []string{"v"},
				Volumes: []Volume{
					{Name: "a", Length: 2}, {Name: "b", Length: 8},
					{Name: "v", Length: 8, Segments: []Segment{
						Stripe(0, 8, PermReadWrite, 2, "a", 0, "b", 0),
					}},
				},
			},
			want: ErrOverflow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.g)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	leaf := Volume{Name: "leaf", Length: 1}
	g := Graph{Roots: []string{"a"}, Volumes: []Volume{leaf}}
	for i := 0; i < MaxLayers; i++ {
		name := string(rune('a' + i))
		if i == MaxLayers-1 {
			g.Volumes = append(g.Volumes, Volume{
				Name: name, Length: 1,
				Segments: []Segment{Linear(0, 1, PermReadWrite, "leaf", 0)},
			})
		} else {
			next := string(rune('a' + i + 1))
			g.Volumes = append(g.Volumes, Volume{
				Name: name, Length: 1,
				Segments: []Segment{Linear(0, 1, PermReadWrite, next, 0)},
			})
		}
	}
	if _, err := Compile(g); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("deep graph err = %v, want %v", err, ErrTooDeep)
	}
}

func TestReachableCycleRejected(t *testing.T) {
	g := Graph{
		Roots: []string{"a"},
		Volumes: []Volume{
			{Name: "a", Length: 1, Segments: []Segment{Linear(0, 1, PermReadWrite, "b", 0)}},
			{Name: "b", Length: 1, Segments: []Segment{Linear(0, 1, PermReadWrite, "a", 0)}},
		},
	}
	if _, err := Compile(g); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want %v", err, ErrCycle)
	}
}

func TestReadOnlyAliasCannotBecomeWritable(t *testing.T) {
	g := Graph{
		Roots:    []string{"snapshot", "live"},
		Physical: []PhysicalRange{{Start: 500, Length: 4, Permission: PermReadWrite}},
		Volumes: []Volume{
			{Name: "ro", Length: 4, Segments: []Segment{Physical(0, 4, PermRead, 500)}},
			{Name: "snapshot", Length: 4, Segments: []Segment{Linear(0, 4, PermReadWrite, "ro", 0)}},
			{Name: "rw", Length: 4, Segments: []Segment{Physical(0, 4, PermReadWrite, 500)}},
			{Name: "live", Length: 4, Segments: []Segment{Linear(0, 4, PermReadWrite, "rw", 0)}},
		},
		Isolations: []IsolationPair{{A: "snapshot", B: "live"}},
	}
	snap, err := Compile(g)
	if err != nil {
		t.Fatalf("read-only alias should not count as writable isolation: %v", err)
	}
	roProof, err := snap.Proof("snapshot", 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if roProof.Writable {
		t.Fatal("read-only alias was reported writable")
	}
	if _, ok, err := snap.CheckWrite("live", 0, 4); err != nil || !ok {
		t.Fatalf("live write: ok=%v err=%v", ok, err)
	}
}

func TestIsolationLastStripeOnly(t *testing.T) {
	const w = uint64(1 << 20)
	base := MaxSectors - 5*w
	makeGraph := func(bEven uint64) Graph {
		return Graph{
			Roots: []string{"A", "B"},
			Physical: []PhysicalRange{
				{Start: base, Length: 2 * w, Permission: PermReadWrite},
				{Start: base + 2*w, Length: w, Permission: PermReadWrite},
				{Start: base + 3*w, Length: w, Permission: PermReadWrite},
				{Start: base + 4*w, Length: w, Permission: PermReadWrite},
			},
			Volumes: []Volume{
				{Name: "AE", Length: 2 * w, Segments: []Segment{Physical(0, 2*w, PermReadWrite, base)}},
				{Name: "AO", Length: w, Segments: []Segment{Physical(0, w, PermReadWrite, base+2*w)}},
				{Name: "A", Length: 3 * w, Segments: []Segment{
					Stripe(0, 3*w, PermReadWrite, w, "AE", 0, "AO", 0),
				}},
				{Name: "BE", Length: w, Segments: []Segment{Physical(0, w, PermReadWrite, bEven)}},
				{Name: "BO", Length: w, Segments: []Segment{Physical(0, w, PermReadWrite, base+4*w)}},
				{Name: "B", Length: 2 * w, Segments: []Segment{
					Stripe(0, 2*w, PermReadWrite, w, "BE", 0, "BO", 0),
				}},
			},
			Isolations: []IsolationPair{{A: "A", B: "B"}},
		}
	}

	// A's last stripe (stripe 2, even) is [base+w,base+2w). B's first even
	// stripe is deliberately placed exactly there, so only that stripe overlaps.
	if _, err := Compile(makeGraph(base + w)); !errors.Is(err, ErrIsolation) {
		t.Fatalf("last-stripe overlap err = %v, want %v", err, ErrIsolation)
	}

	// Moving B's even child clears the one-stripe intersection.
	g := makeGraph(base + 3*w)
	snap, err := Compile(g)
	if err != nil {
		t.Fatalf("non-overlapping large graph: %v", err)
	}
	if _, ok, err := snap.CheckWrite("A", 2*w, w); err != nil || !ok {
		t.Fatalf("A last stripe write: ok=%v err=%v", ok, err)
	}
	if _, ok, err := snap.CheckWrite("B", 0, w); err != nil || !ok {
		t.Fatalf("B first stripe write: ok=%v err=%v", ok, err)
	}
}

func TestStoreVersionsAndAtomicIsolation(t *testing.T) {
	g100 := versionGraph(100)
	store := NewStore()
	v1, err := store.Publish(g100)
	if err != nil {
		t.Fatal(err)
	}
	old, err := v1.Handle("v")
	if err != nil {
		t.Fatal(err)
	}
	assertPhysical(t, old, 100)

	g300 := versionGraph(300)
	v2, err := store.Publish(g300)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version() <= v1.Version() {
		t.Fatalf("versions did not increase: %d then %d", v1.Version(), v2.Version())
	}
	assertPhysical(t, old, 100)
	current, err := store.Handle("v")
	if err != nil {
		t.Fatal(err)
	}
	assertPhysical(t, current, 300)

	bad := Graph{
		Roots:    []string{"va", "vb"},
		Physical: []PhysicalRange{{Start: 400, Length: 4, Permission: PermReadWrite}},
		Volumes: []Volume{
			{Name: "pa", Length: 4, Segments: []Segment{Physical(0, 4, PermReadWrite, 400)}},
			{Name: "va", Length: 4, Segments: []Segment{Linear(0, 4, PermReadWrite, "pa", 0)}},
			{Name: "pb", Length: 4, Segments: []Segment{Physical(0, 4, PermReadWrite, 400)}},
			{Name: "vb", Length: 4, Segments: []Segment{Linear(0, 4, PermReadWrite, "pb", 0)}},
		},
		Isolations: []IsolationPair{{A: "va", B: "vb"}},
	}
	if _, err := store.Publish(bad); !errors.Is(err, ErrIsolation) {
		t.Fatalf("bad publish err = %v, want %v", err, ErrIsolation)
	}
	if got := store.Current().Version(); got != v2.Version() {
		t.Fatalf("failed publish changed current version to %d", got)
	}
	stillCurrent, err := store.Handle("v")
	if err != nil {
		t.Fatal(err)
	}
	assertPhysical(t, stillCurrent, 300)
}

func smallGraph() Graph {
	return Graph{
		Roots: []string{"root"},
		Physical: []PhysicalRange{
			{Start: 100, Length: 8, Permission: PermReadWrite},
			{Start: 300, Length: 8, Permission: PermReadWrite},
		},
		Volumes: []Volume{
			{Name: "A", Length: 4, Segments: []Segment{Physical(0, 4, PermReadWrite, 100)}},
			{Name: "B", Length: 4, Segments: []Segment{Physical(0, 4, PermReadWrite, 300)}},
			{Name: "mid", Length: 8, Segments: []Segment{
				Stripe(0, 8, PermReadWrite, 2, "A", 0, "B", 0),
			}},
			{Name: "snap", Length: 4, Segments: []Segment{Physical(0, 4, PermRead, 100)}},
			{Name: "root", Length: 12, Segments: []Segment{
				Linear(0, 8, PermReadWrite, "mid", 0),
				Linear(8, 4, PermReadWrite, "snap", 0),
			}},
		},
	}
}

func versionGraph(physicalStart uint64) Graph {
	return Graph{
		Roots:    []string{"v"},
		Physical: []PhysicalRange{{Start: physicalStart, Length: 4, Permission: PermReadWrite}},
		Volumes: []Volume{{
			Name: "v", Length: 4,
			Segments: []Segment{Physical(0, 4, PermReadWrite, physicalStart)},
		}},
	}
}

func mustVolume(t *testing.T, g Graph, name string) Volume {
	t.Helper()
	for _, v := range g.Volumes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("volume %q not found", name)
	return Volume{}
}

func resolveSector(g Graph, volume string, addr uint64) (uint64, Permission, error) {
	perms := make(map[string]Volume, len(g.Volumes))
	for _, v := range g.Volumes {
		perms[v.Name] = v
	}
	var walk func(string, uint64, Permission, int) (uint64, Permission, error)
	walk = func(name string, at uint64, eff Permission, depth int) (uint64, Permission, error) {
		if depth > MaxLayers {
			return 0, 0, ErrTooDeep
		}
		v, ok := perms[name]
		if !ok {
			return 0, 0, ErrNotFound
		}
		for _, s := range v.Segments {
			if at < s.VirtualStart || at >= s.VirtualStart+s.Length {
				continue
			}
			eff &= s.Permission
			local := at - s.VirtualStart
			switch s.Mapping.Kind {
			case MapLinear:
				return walk(s.Mapping.Child, s.Mapping.ChildOffset+local, eff, depth+1)
			case MapStripe:
				stripe := local / s.Mapping.StripeWidth
				within := local % s.Mapping.StripeWidth
				if stripe%2 == 0 {
					child := s.Mapping.LeftOffset + (stripe/2)*s.Mapping.StripeWidth + within
					return walk(s.Mapping.Left, child, eff, depth+1)
				}
				child := s.Mapping.RightOffset + (stripe/2)*s.Mapping.StripeWidth + within
				return walk(s.Mapping.Right, child, eff, depth+1)
			case MapPhysical:
				phys := s.Mapping.PhysicalStart + local
				for _, r := range g.Physical {
					if phys >= r.Start && phys < r.Start+r.Length {
						return phys, eff & r.Permission, nil
					}
				}
				return 0, 0, ErrNotOwned
			default:
				return 0, 0, ErrInvalid
			}
		}
		return 0, 0, ErrUnmapped
	}
	return walk(volume, addr, PermReadWrite, 1)
}

func expandSmallClass(t *testing.T, c Class) []uint64 {
	t.Helper()
	flat, err := flattenClass(c, 1024)
	if err != nil {
		t.Fatalf("flatten class: %v", err)
	}
	var out []uint64
	for _, x := range flat {
		n, ok := x.Size()
		if !ok || n > 1024 {
			t.Fatalf("class too large for small expansion: %+v", x)
		}
		for i := uint64(0); i < x.Count; i++ {
			for j := uint64(0); j < x.Run; j++ {
				out = append(out, x.Start+i*x.Stride+j)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func classContains(c Class, sector uint64) (bool, int) {
	flat, err := flattenClass(c, 1024)
	if err != nil {
		return false, 0
	}
	n := 0
	for _, x := range flat {
		if sector < x.Start {
			continue
		}
		delta := sector - x.Start
		if delta%x.Stride >= x.Run {
			continue
		}
		if delta/x.Stride < x.Count {
			n++
		}
	}
	return n != 0, n
}

func classContainsExactly(c Class, sector uint64) bool {
	ok, n := classContains(c, sector)
	return ok && n == 1
}

func checkProofShape(t *testing.T, p Proof, length uint64) {
	t.Helper()
	var total uint64
	seen := make(map[uint64]bool)
	for _, c := range p.Claims {
		n, ok := c.Virtual.Size()
		if !ok {
			t.Fatal("virtual size overflow")
		}
		total += n
		for _, v := range expandSmallClass(t, c.Virtual) {
			if seen[v] {
				t.Fatalf("virtual sector %d is covered twice; claims=%+v", v, p.Claims)
			}
			seen[v] = true
		}
	}
	if total != length || uint64(len(seen)) != length {
		t.Fatalf("proof total=%d unique=%d want=%d", total, len(seen), length)
	}
}

func checkClaimPath(t *testing.T, c Claim) {
	t.Helper()
	if len(c.Path) == 0 {
		t.Fatal("claim has empty path")
	}
	for i, step := range c.Path {
		if step.After&^step.Before != 0 {
			t.Fatalf("step %d broadened permission: %s -> %s", i, step.Before, step.After)
		}
		if step.Narrowed != (step.Before != step.After) {
			t.Fatalf("step %d narrowed flag is wrong: %+v", i, step)
		}
		if step.Mapping.Kind != step.Kind {
			t.Fatalf("step %d kind/mapping mismatch: %+v", i, step)
		}
		want := step.Before & step.SegmentPerm
		if i == len(c.Path)-1 && step.Kind == MapPhysical {
			want &= c.TenantPermission
		}
		if step.After != want {
			t.Fatalf("step %d permission %s, want %s (%+v)", i, step.After, want, step)
		}
	}
	if c.Path[len(c.Path)-1].Kind != MapPhysical {
		t.Fatalf("claim path does not end at a physical leaf")
	}
	if c.Access != c.Path[len(c.Path)-1].After {
		t.Fatalf("claim access %s != last step %s", c.Access, c.Path[len(c.Path)-1].After)
	}
}

func assertPhysical(t *testing.T, h *Handle, want uint64) {
	t.Helper()
	p, err := h.Proof(0, 1)
	if err != nil {
		t.Fatalf("handle proof: %v", err)
	}
	if len(p.Claims) != 1 || !classContainsExactly(p.Claims[0].Physical, want) {
		t.Fatalf("handle physical = %+v, want %d", p.Claims, want)
	}
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v != 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
