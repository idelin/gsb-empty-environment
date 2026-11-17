package netsec

import (
	"errors"
	"math/rand/v2"
	"testing"
)

// enumerateBruteForce lists every legal layout by exhaustive recursion over
// the candidate start pages. It is the independent reference implementation
// used to check the memoized counter and rank/unrank routines.
func enumerateBruteForce(s *Spec) []Layout {
	if err := s.Validate(); err != nil {
		panic(err)
	}
	var reserved uint64
	for _, page := range s.Reserved {
		reserved |= uint64(1) << uint(page)
	}
	type choice struct {
		start int
		mask  uint64
	}
	perRegion := make([][]choice, len(s.Regions))
	for idx, region := range s.Regions {
		for start := 0; start+region.Size <= s.Pages; start++ {
			if start%region.Alignment != 0 {
				continue
			}
			mask := (uint64(1)<<uint(region.Size) - 1) << uint(start)
			if mask&reserved != 0 {
				continue
			}
			perRegion[idx] = append(perRegion[idx], choice{start: start, mask: mask})
		}
	}
	var out []Layout
	current := make(Layout, len(s.Regions))
	var recurse func(i int, occupied uint64)
	recurse = func(i int, occupied uint64) {
		if i == len(s.Regions) {
			copied := make(Layout, len(current))
			copy(copied, current)
			out = append(out, copied)
			return
		}
		for _, choice := range perRegion[i] {
			if choice.mask&occupied != 0 {
				continue
			}
			current[i] = choice.start
			recurse(i+1, occupied|choice.mask)
		}
	}
	recurse(0, 0)
	return out
}

func smallSpecs() []*Spec {
	specs := []*Spec{
		{Pages: 1, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 2, Regions: []Region{{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}}},
		{Pages: 3, Reserved: []int{1}, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 4, Regions: []Region{{Size: 2, Alignment: 2}, {Size: 1, Alignment: 1}}},
		{Pages: 5, Reserved: []int{0, 4}, Regions: []Region{{Size: 2, Alignment: 2}, {Size: 1, Alignment: 1}}},
		{Pages: 6, Regions: []Region{{Size: 3, Alignment: 1}, {Size: 2, Alignment: 2}, {Size: 1, Alignment: 1}}},
		{Pages: 6, Regions: []Region{
			{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
			{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
		}},
		{Pages: 8, Reserved: []int{3}, Regions: []Region{
			{Size: 2, Alignment: 4}, {Size: 2, Alignment: 2},
			{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
		}},
		{Pages: 7, Reserved: []int{2}, Regions: []Region{{Size: 4, Alignment: 1}, {Size: 2, Alignment: 2}}},
	}
	for pages := 1; pages <= 6; pages++ {
		for nReg := 1; nReg <= 3; nReg++ {
			regions := make([]Region, nReg)
			for i := range regions {
				regions[i] = Region{Size: 1, Alignment: 1}
			}
			specs = append(specs, &Spec{Pages: pages, Regions: regions})
			if pages >= 4 {
				aligned := make([]Region, nReg)
				for i := range aligned {
					aligned[i] = Region{Size: 2, Alignment: 2}
				}
				specs = append(specs, &Spec{
					Pages:    pages,
					Reserved: []int{pages / 2},
					Regions:  aligned,
				})
			}
		}
	}
	return specs
}

func sameLayout(a, b Layout) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCountRankUnrankExhaustive(t *testing.T) {
	for idx, spec := range smallSpecs() {
		want := enumerateBruteForce(spec)
		got, err := spec.Count()
		if err != nil {
			t.Fatalf("spec %d: unexpected count error: %v", idx, err)
		}
		if got != int64(len(want)) {
			t.Fatalf("spec %d: count = %d, brute force = %d", idx, got, len(want))
		}
		for rank, layout := range want {
			gotRank, err := spec.Rank(layout)
			if err != nil {
				t.Fatalf("spec %d: rank error for %v: %v", idx, layout, err)
			}
			if gotRank != int64(rank) {
				t.Fatalf("spec %d: layout %v rank = %d, want %d", idx, layout, gotRank, rank)
			}
			gotLayout, err := spec.Unrank(int64(rank))
			if err != nil {
				t.Fatalf("spec %d: unrank error for %d: %v", idx, rank, err)
			}
			if !sameLayout(gotLayout, layout) {
				t.Fatalf("spec %d: unrank(%d) = %v, want %v", idx, rank, gotLayout, layout)
			}
		}
		if len(want) > 0 {
			if _, err := spec.Unrank(int64(len(want))); !errors.Is(err, ErrInvalidRank) {
				t.Fatalf("spec %d: expected ErrInvalidRank, got %v", idx, err)
			}
			if _, err := spec.Unrank(-1); !errors.Is(err, ErrInvalidRank) {
				t.Fatalf("spec %d: expected ErrInvalidRank, got %v", idx, err)
			}
			illegal := make(Layout, len(spec.Regions))
			copy(illegal, want[0])
			illegal[0] = spec.Pages
			if _, err := spec.Rank(illegal); !errors.Is(err, ErrInvalidLayout) {
				t.Fatalf("spec %d: out-of-range layout expected ErrInvalidLayout, got %v", idx, err)
			}
			if len(spec.Regions) > 1 {
				short := Layout{want[0][0]}
				if _, err := spec.Rank(short); !errors.Is(err, ErrInvalidLayout) {
					t.Fatalf("spec %d: short layout expected ErrInvalidLayout, got %v", idx, err)
				}
			}
		}
	}
}

func TestZeroSolutions(t *testing.T) {
	specs := []*Spec{
		{Pages: 2, Reserved: []int{0, 1}, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 4, Regions: []Region{{Size: 3, Alignment: 1}, {Size: 3, Alignment: 1}}},
		{Pages: 3, Reserved: []int{0, 1}, Regions: []Region{{Size: 2, Alignment: 1}}},
	}
	for idx, spec := range specs {
		count, err := spec.Count()
		if err != nil {
			t.Fatalf("spec %d: unexpected error: %v", idx, err)
		}
		if count != 0 {
			t.Fatalf("spec %d: expected zero layouts, got %d", idx, count)
		}
		if _, err := spec.Unrank(0); !errors.Is(err, ErrNoLayout) {
			t.Fatalf("spec %d: Unrank expected ErrNoLayout, got %v", idx, err)
		}
		stream := NewStream(&byteReader{data: []byte{1, 2, 3, 4, 5, 6, 7, 8}})
		if _, err := spec.Sample(stream); !errors.Is(err, ErrNoLayout) {
			t.Fatalf("spec %d: Sample expected ErrNoLayout, got %v", idx, err)
		}
		if pos := stream.Position(); pos != 0 {
			t.Fatalf("spec %d: zero-solution sample consumed %d random bytes", idx, pos)
		}
	}
}

type byteReader struct {
	data []byte
}

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errStreamExhausted
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

var errStreamExhausted = errors.New("netsec test: random stream exhausted")

type scriptedReader struct {
	data []byte
	err  error
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

var errCanceled = errors.New("netsec test: stream canceled")

func TestSampleCancellationStreamPosition(t *testing.T) {
	spec := &Spec{Pages: 4, Regions: []Region{{Size: 1, Alignment: 1}}}

	// Immediate cancellation: no bytes delivered, position stays at zero.
	stream := NewStream(&scriptedReader{err: errCanceled})
	if _, err := spec.Sample(stream); !errors.Is(err, errCanceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if pos := stream.Position(); pos != 0 {
		t.Fatalf("expected position 0 after immediate cancel, got %d", pos)
	}

	// Cancellation mid-draw: 32*31*30 = 29760 layouts give a two-byte index
	// width; the first byte is delivered and the second read fails.
	wideSpec := &Spec{Pages: 32, Regions: []Region{
		{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
	}}
	stream = NewStream(&scriptedReader{data: []byte{7}, err: errStreamExhausted})
	if got, err := wideSpec.Sample(stream); !errors.Is(err, errStreamExhausted) {
		t.Fatalf("expected exhausted-stream error, got %v (layout %v pos %d)", err, got, stream.Position())
	}
	if pos := stream.Position(); pos != 1 {
		t.Fatalf("expected position 1 after partial read, got %d", pos)
	}

	// Same semantics on a three-page domain: 255 is a rejected integer, so
	// the following draw reaches the canceled source after one byte.
	threePage := &Spec{Pages: 3, Regions: []Region{{Size: 1, Alignment: 1}}}
	stream = NewStream(&scriptedReader{data: []byte{255}, err: errCanceled})
	if _, err := threePage.Sample(stream); !errors.Is(err, errCanceled) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if pos := stream.Position(); pos != 1 {
		t.Fatalf("expected position 1 after canceled second draw, got %d", pos)
	}

	// Unique layout: sampling is deterministic and consumes no randomness.
	unique := &Spec{Pages: 1, Regions: []Region{{Size: 1, Alignment: 1}}}
	stream = NewStream(&scriptedReader{err: errCanceled})
	layout, err := unique.Sample(stream)
	if err != nil {
		t.Fatalf("single-layout sample failed: %v", err)
	}
	if layout[0] != 0 || stream.Position() != 0 {
		t.Fatalf("single-layout sample = %v pos %d, want [0] pos 0", layout, stream.Position())
	}
}

func TestRejectionSamplingPositions(t *testing.T) {
	// n = 3 needs one byte per draw; cutoff = 255 - 255%3 = 255, so 255 is
	// rejected and a fresh byte must be drawn. Both bytes are consumed.
	spec := &Spec{Pages: 3, Regions: []Region{{Size: 1, Alignment: 1}}}
	stream := NewStream(&byteReader{data: []byte{255, 0}})
	layout, err := spec.Sample(stream)
	if err != nil {
		t.Fatalf("sample failed: %v", err)
	}
	if layout[0] != 0 {
		t.Fatalf("rejected draw then 0 should yield start 0, got %d", layout[0])
	}
	if pos := stream.Position(); pos != 2 {
		t.Fatalf("rejection should consume 2 bytes, got position %d", pos)
	}

	// Accepted values map bijectively via modulo, one byte each.
	for value := byte(0); value < 3; value++ {
		oneByte := NewStream(&byteReader{data: []byte{value}})
		got, err := spec.Sample(oneByte)
		if err != nil {
			t.Fatalf("sample failed: %v", err)
		}
		if got[0] != int(value) || oneByte.Position() != 1 {
			t.Fatalf("value %d: got layout %v pos %d", value, got, oneByte.Position())
		}
	}
}

func TestSampleUniformity(t *testing.T) {
	spec := &Spec{Pages: 4, Regions: []Region{{Size: 1, Alignment: 1}}}
	count, err := spec.Count()
	if err != nil || count != 4 {
		t.Fatalf("unexpected count %d err %v", count, err)
	}
	rng := rand.New(rand.NewPCG(0x1234, 0x5678))
	random := make([]byte, 40000)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	stream := NewStream(&byteReader{data: random})
	freq := make([]int, count)
	const draws = 8000
	for i := 0; i < draws; i++ {
		layout, err := spec.Sample(stream)
		if err != nil {
			t.Fatalf("draw %d failed: %v", i, err)
		}
		rank, err := spec.Rank(layout)
		if err != nil {
			t.Fatalf("rank failed: %v", err)
		}
		freq[rank]++
	}
	for rank, n := range freq {
		if n == 0 {
			t.Fatalf("layout rank %d never drawn", rank)
		}
		// Expected frequency is 2000; reject deviations beyond 12%.
		if n < 1760 || n > 2240 {
			t.Fatalf("rank %d frequency %d outside tolerance", rank, n)
		}
	}
}

func TestFullDomain32Pages(t *testing.T) {
	spec := &Spec{Pages: 32, Regions: []Region{
		{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
		{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
	}}
	count, err := spec.Count()
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	// Six labeled single pages out of 32: 32 * 31 * ... * 27.
	const want = int64(32 * 31 * 30 * 29 * 28 * 27)
	if count != want {
		t.Fatalf("full-domain count = %d, want %d", count, want)
	}
	// Every rank must round-trip through unrank/rank.
	first, err := spec.Unrank(0)
	if err != nil {
		t.Fatalf("unrank 0 failed: %v", err)
	}
	if rank, err := spec.Rank(first); err != nil || rank != 0 {
		t.Fatalf("first layout rank = %d err %v, want 0", rank, err)
	}
	last, err := spec.Unrank(count - 1)
	if err != nil {
		t.Fatalf("unrank last failed: %v", err)
	}
	if rank, err := spec.Rank(last); err != nil || rank != count-1 {
		t.Fatalf("last layout rank = %d err %v, want %d", rank, err, count-1)
	}

	// Sampling works on the full-size domain with a 4-byte index width.
	rng := rand.New(rand.NewPCG(0x9e3779b9, 0x85ebca6b))
	random := make([]byte, 4000)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	stream := NewStream(&byteReader{data: random})
	for i := 0; i < 30; i++ {
		layout, err := spec.Sample(stream)
		if err != nil {
			t.Fatalf("draw %d failed: %v", i, err)
		}
		if rank, err := spec.Rank(layout); err != nil || rank < 0 || rank >= count {
			t.Fatalf("draw %d produced invalid rank %d: %v", i, rank, err)
		}
	}
	if pos := stream.Position(); pos == 0 || pos%4 != 0 {
		t.Fatalf("expected nonzero 4-byte-aligned stream position, got %d", pos)
	}
}

func TestValidation(t *testing.T) {
	bad := []*Spec{
		nil,
		{Pages: 0, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 33, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 4, Regions: nil},
		{Pages: 4, Regions: []Region{
			{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
			{Size: 1, Alignment: 1}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
			{Size: 1, Alignment: 1},
		}},
		{Pages: 4, Regions: []Region{{Size: 0, Alignment: 1}}},
		{Pages: 4, Regions: []Region{{Size: 1, Alignment: 3}}},
		{Pages: 4, Reserved: []int{4}, Regions: []Region{{Size: 1, Alignment: 1}}},
		{Pages: 4, Reserved: []int{-1}, Regions: []Region{{Size: 1, Alignment: 1}}},
	}
	for idx, spec := range bad {
		if err := spec.Validate(); err == nil {
			t.Fatalf("bad spec %d unexpectedly valid", idx)
		}
	}
}

func BenchmarkCount32Pages(b *testing.B) {
	spec := &Spec{Pages: 32, Regions: []Region{
		{Size: 2, Alignment: 2}, {Size: 1, Alignment: 1}, {Size: 1, Alignment: 1},
		{Size: 3, Alignment: 1}, {Size: 1, Alignment: 4}, {Size: 1, Alignment: 1},
	}}
	for i := 0; i < b.N; i++ {
		if _, err := spec.Count(); err != nil {
			b.Fatal(err)
		}
	}
}
