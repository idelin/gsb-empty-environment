package netsec

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// covers reports whether addr is inside seg's mapped range, including
// zero-fill. The subtraction form avoids overflow on huge addresses.
func covers(s Segment, addr uint64) bool {
	return s.MemSize > 0 && s.VAddr <= addr && addr-s.VAddr < s.MemSize
}

// refAnalyze is the byte-by-byte reference: it computes the permission of
// every byte in [0, limit), folds bytes into pages, and derives dangerous
// pages and minimal sets independently of the sweep in Analyze.
func refAnalyze(segs []Segment, pageSize, limit uint64) (map[uint64]Perm, []SegmentSet) {
	pagePerm := make(map[uint64]Perm)
	pageActive := make(map[uint64][]int)
	for addr := uint64(0); addr < limit; addr++ {
		pg := addr / pageSize
		var p Perm
		for i, s := range segs {
			if covers(s, addr) {
				p |= s.Perm
				if !containsInt(pageActive[pg], i) {
					pageActive[pg] = append(pageActive[pg], i)
				}
			}
		}
		pagePerm[pg] |= p
	}
	danger := make(map[uint64]Perm)
	seen := make(map[string]bool)
	var sets []SegmentSet
	for pg, p := range pagePerm {
		if !p.Dangerous() {
			continue
		}
		danger[pg] = p
		active := append([]int(nil), pageActive[pg]...)
		sort.Ints(active)
		for _, set := range bruteMinimalSets(segs, active) {
			k := keyOf(set)
			if seen[k] {
				continue
			}
			seen[k] = true
			var up Perm
			for _, idx := range set {
				up |= segs[idx].Perm
			}
			sets = append(sets, SegmentSet{Segments: set, Perm: up})
		}
	}
	sortSets(sets)
	return danger, sets
}

// bruteMinimalSets enumerates every subset of active and keeps the
// dangerous ones with no dangerous proper subset.
func bruteMinimalSets(segs []Segment, active []int) [][]int {
	danger := func(mask uint64) bool {
		var p Perm
		for b, idx := range active {
			if mask&(1<<uint(b)) != 0 {
				p |= segs[idx].Perm
			}
		}
		return p.Dangerous()
	}
	var out [][]int
	n := len(active)
	for mask := uint64(1); mask < 1<<uint(n); mask++ {
		if !danger(mask) {
			continue
		}
		minimal := true
		for sub := mask; sub > 0; sub = (sub - 1) & mask {
			if sub != mask && danger(sub) {
				minimal = false
				break
			}
		}
		if minimal {
			var set []int
			for b := 0; b < n; b++ {
				if mask&(1<<uint(b)) != 0 {
					set = append(set, active[b])
				}
			}
			out = append(out, set)
		}
	}
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func sortSets(sets []SegmentSet) {
	sort.Slice(sets, func(i, j int) bool {
		a, b := sets[i].Segments, sets[j].Segments
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
}

// dangerMap expands a Report's ranges into a per-page permission map.
func dangerMap(t *testing.T, rep Report) map[uint64]Perm {
	t.Helper()
	out := make(map[uint64]Perm)
	for _, d := range rep.Dangerous {
		first := d.Start / rep.PageSize
		for pg := first; pg < first+d.Pages; pg++ {
			if _, dup := out[pg]; dup {
				t.Fatalf("overlapping danger ranges at page %d", pg)
			}
			out[pg] = d.Perm
		}
	}
	return out
}

func checkAgainstRef(t *testing.T, segs []Segment, pageSize, limit uint64) {
	t.Helper()
	rep, err := Analyze(segs, pageSize)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	wantDanger, wantSets := refAnalyze(segs, pageSize, limit)
	if got := dangerMap(t, rep); !reflect.DeepEqual(got, wantDanger) {
		t.Fatalf("dangerous pages mismatch:\n got %v\nwant %v", got, wantDanger)
	}
	if rep.DangerousPages() != uint64(len(wantDanger)) {
		t.Fatalf("DangerousPages = %d, want %d", rep.DangerousPages(), len(wantDanger))
	}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("minimal sets mismatch:\n got %+v\nwant %+v", rep.Sets, wantSets)
	}
}

func TestSharedPageComposition(t *testing.T) {
	// Neither segment is dangerous alone; their shared page is RWX.
	segs := []Segment{
		{VAddr: 1, FileOff: 1, FileSize: 3, MemSize: 3, Perm: PermR | PermX},
		{VAddr: 2, FileOff: 2, FileSize: 6, MemSize: 6, Perm: PermR | PermW},
	}
	rep, err := Analyze(segs, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []DangerRange{{Start: 0, Pages: 1, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Dangerous, want) {
		t.Fatalf("Dangerous = %+v, want %+v", rep.Dangerous, want)
	}
	wantSets := []SegmentSet{{Segments: []int{0, 1}, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("Sets = %+v, want %+v", rep.Sets, wantSets)
	}
	checkAgainstRef(t, segs, 4, 16)
}

func TestSingleWXSegment(t *testing.T) {
	segs := []Segment{
		{VAddr: 8, FileOff: 0, FileSize: 8, MemSize: 8, Perm: PermR | PermW | PermX},
	}
	rep, err := Analyze(segs, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []DangerRange{{Start: 8, Pages: 2, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Dangerous, want) {
		t.Fatalf("Dangerous = %+v, want %+v", rep.Dangerous, want)
	}
	wantSets := []SegmentSet{{Segments: []int{0}, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("Sets = %+v, want %+v", rep.Sets, wantSets)
	}
	checkAgainstRef(t, segs, 4, 24)
}

func TestBSSZeroFillCounts(t *testing.T) {
	// Segment 0's BSS tail [8,10) shares page 1 with the RW segment;
	// ignoring zero-fill would miss the RWX page.
	segs := []Segment{
		{VAddr: 4, FileOff: 4, FileSize: 2, MemSize: 6, Perm: PermR | PermX},
		{VAddr: 8, FileOff: 8, FileSize: 8, MemSize: 8, Perm: PermR | PermW},
	}
	rep, err := Analyze(segs, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []DangerRange{{Start: 8, Pages: 1, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Dangerous, want) {
		t.Fatalf("Dangerous = %+v, want %+v", rep.Dangerous, want)
	}
	wantSets := []SegmentSet{{Segments: []int{0, 1}, Perm: PermR | PermW | PermX}}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("Sets = %+v, want %+v", rep.Sets, wantSets)
	}
	checkAgainstRef(t, segs, 8, 24)
}

func TestBSSBoundaryExact(t *testing.T) {
	// Same shape, but the RX segment ends exactly at the page boundary:
	// no shared page, no danger.
	segs := []Segment{
		{VAddr: 4, FileOff: 4, FileSize: 4, MemSize: 4, Perm: PermR | PermX},
		{VAddr: 8, FileOff: 8, FileSize: 8, MemSize: 8, Perm: PermR | PermW},
	}
	rep, err := Analyze(segs, 8)
	if err != nil {
		t.Fatal(err)
	}
	if rep.HasDanger() {
		t.Fatalf("unexpected danger: %+v", rep.Dangerous)
	}
	checkAgainstRef(t, segs, 8, 24)
}

func TestMinimalSetsSingletonAndPair(t *testing.T) {
	// A WX segment, a W-only segment and an X-only segment all cover
	// page 0: both {0} and {1,2} are minimal dangerous sets.
	segs := []Segment{
		{VAddr: 0, FileOff: 0, FileSize: 2, MemSize: 2, Perm: PermW | PermX},
		{VAddr: 1, FileOff: 1, FileSize: 2, MemSize: 2, Perm: PermW},
		{VAddr: 2, FileOff: 2, FileSize: 2, MemSize: 2, Perm: PermX},
	}
	rep, err := Analyze(segs, 4)
	if err != nil {
		t.Fatal(err)
	}
	wantSets := []SegmentSet{
		{Segments: []int{0}, Perm: PermW | PermX},
		{Segments: []int{1, 2}, Perm: PermW | PermX},
	}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("Sets = %+v, want %+v", rep.Sets, wantSets)
	}
	checkAgainstRef(t, segs, 4, 8)
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		segs []Segment
		ps   uint64
		want error
	}{
		{"page size zero", nil, 0, ErrPageSize},
		{"size order", []Segment{{VAddr: 0, FileSize: 5, MemSize: 4}}, 4, ErrSizeOrder},
		{"overflow wrap", []Segment{{VAddr: math.MaxUint64 - 3, MemSize: 5}}, 4, ErrOverflow},
		{"overflow to zero", []Segment{{VAddr: math.MaxUint64, MemSize: 1}}, 4, ErrOverflow},
		{"overflow exact 2^64", []Segment{{VAddr: math.MaxUint64 - 3, MemSize: 4}}, 4, ErrOverflow},
		{"misaligned", []Segment{{VAddr: 10, FileOff: 3, FileSize: 4, MemSize: 4}}, 4, ErrMisaligned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Analyze(tc.segs, tc.ps); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHighAddressValid(t *testing.T) {
	// End exactly at MaxUint64: valid, and page math must not overflow.
	segs := []Segment{
		{VAddr: math.MaxUint64 - 3, MemSize: 3, Perm: PermW | PermX},
	}
	rep, err := Analyze(segs, 4)
	if err != nil {
		t.Fatal(err)
	}
	start := uint64(math.MaxUint64-3) / 4 * 4
	want := []DangerRange{{Start: start, Pages: 1, Perm: PermW | PermX}}
	if !reflect.DeepEqual(rep.Dangerous, want) {
		t.Fatalf("Dangerous = %+v, want %+v", rep.Dangerous, want)
	}
	wantSets := []SegmentSet{{Segments: []int{0}, Perm: PermW | PermX}}
	if !reflect.DeepEqual(rep.Sets, wantSets) {
		t.Fatalf("Sets = %+v, want %+v", rep.Sets, wantSets)
	}
}

func TestEdgeCases(t *testing.T) {
	// Empty input and zero-length segments map nothing.
	for _, segs := range [][]Segment{
		nil,
		{{VAddr: 4, FileOff: 4, Perm: PermW | PermX}},
	} {
		rep, err := Analyze(segs, 4)
		if err != nil {
			t.Fatal(err)
		}
		if rep.HasDanger() || len(rep.Sets) != 0 {
			t.Fatalf("expected empty report, got %+v", rep)
		}
	}
	// BSS-only segment skips the file-offset congruence check.
	segs := []Segment{{VAddr: 3, FileOff: 7, FileSize: 0, MemSize: 9, Perm: PermW | PermX}}
	rep, err := Analyze(segs, 4)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DangerousPages() != 3 {
		t.Fatalf("DangerousPages = %d, want 3", rep.DangerousPages())
	}
}

func TestPermString(t *testing.T) {
	cases := map[Perm]string{
		0:                     "---",
		PermR:                 "r--",
		PermW:                 "-w-",
		PermX:                 "--x",
		PermR | PermW:         "rw-",
		PermR | PermX:         "r-x",
		PermR | PermW | PermX: "rwx",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Fatalf("Perm(%d).String() = %q, want %q", p, got, want)
		}
	}
}

func TestAnalyzeMatchesByteReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 5000; iter++ {
		ps := uint64(1 << rng.Intn(4)) // 1, 2, 4, 8
		if rng.Intn(4) == 0 {
			ps = uint64(1 + rng.Intn(7)) // odd sizes 1..7
		}
		const limit = 48
		segs := make([]Segment, rng.Intn(7))
		for i := range segs {
			v := uint64(rng.Intn(33))
			mem := uint64(rng.Intn(16))
			file := uint64(rng.Intn(int(mem) + 1))
			segs[i] = Segment{
				VAddr:    v,
				FileOff:  v%ps + ps*uint64(rng.Intn(8)), // congruent
				FileSize: file,
				MemSize:  mem,
				Perm:     Perm(rng.Intn(8)),
			}
		}
		rep, err := Analyze(segs, ps)
		if err != nil {
			t.Fatalf("iter %d: Analyze(%+v, %d): %v", iter, segs, ps, err)
		}
		wantDanger, wantSets := refAnalyze(segs, ps, limit)
		if got := dangerMap(t, rep); !reflect.DeepEqual(got, wantDanger) {
			t.Fatalf("iter %d segs=%+v ps=%d:\ndangerous pages got %v\nwant %v",
				iter, segs, ps, got, wantDanger)
		}
		if !reflect.DeepEqual(rep.Sets, wantSets) {
			t.Fatalf("iter %d segs=%+v ps=%d:\nsets got %+v\nwant %+v",
				iter, segs, ps, rep.Sets, wantSets)
		}
	}
}
