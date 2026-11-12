package netsec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// enumerate 是与实现无关的朴素穷举器：递归枚举每个区域的全部对齐起始页，
// 再用独立的合法性检查过滤。它只用于小域，作为 Count/Rank/Unrank 的对照。
func enumerate(spec Spec) []Layout {
	occupied := make([]bool, spec.DomainPages)
	layouts := []Layout{}

	var place func(int, []int)
	place = func(i int, start []int) {
		if i == len(spec.Regions) {
			copied := make([]int, len(start))
			copy(copied, start)
			layouts = append(layouts, Layout{Start: copied})
			return
		}
		region := spec.Regions[i]
		for page := 0; page+region.SizePages <= spec.DomainPages; page += region.AlignPages {
			if !fits(spec, occupied, i, page) {
				continue
			}
			for p := page; p < page+region.SizePages; p++ {
				occupied[p] = true
			}
			place(i+1, append(start, page))
			for p := page; p < page+region.SizePages; p++ {
				occupied[p] = false
			}
		}
	}
	place(0, nil)
	return layouts
}

func fits(spec Spec, occupied []bool, i, page int) bool {
	region := spec.Regions[i]
	for p := page; p < page+region.SizePages; p++ {
		if occupied[p] || isReserved(spec, p) {
			return false
		}
	}
	return true
}

func isReserved(spec Spec, page int) bool {
	for _, reservation := range spec.Reserved {
		if page >= reservation.StartPage && page < reservation.StartPage+reservation.Pages {
			return true
		}
	}
	return false
}

func TestCountMatchesBruteForce(t *testing.T) {
	specs := smallSpecs()
	for idx, spec := range specs {
		got, err := Count(spec)
		if err != nil {
			t.Fatalf("spec #%d: Count error: %v", idx, err)
		}
		want := uint64(len(enumerate(spec)))
		if got != want {
			t.Fatalf("spec #%d: Count = %d, brute force = %d", idx, got, want)
		}
	}
}

func TestRankUnrankBijectWithEnumeration(t *testing.T) {
	for idx, spec := range smallSpecs() {
		total, err := Count(spec)
		if err != nil {
			t.Fatalf("spec #%d: %v", idx, err)
		}
		brute := enumerate(spec)
		if total != uint64(len(brute)) {
			t.Fatalf("spec #%d: count mismatch", idx)
		}

		seen := make(map[uint64]bool, total)
		for _, layout := range brute {
			rank, err := Rank(spec, layout)
			if err != nil {
				t.Fatalf("spec #%d: Rank error: %v", idx, err)
			}
			if rank >= total {
				t.Fatalf("spec #%d: rank %d out of range %d", idx, rank, total)
			}
			if seen[rank] {
				t.Fatalf("spec #%d: duplicate rank %d", idx, rank)
			}
			seen[rank] = true

			got, err := Unrank(spec, rank)
			if err != nil {
				t.Fatalf("spec #%d: Unrank error: %v", idx, err)
			}
			if !sameLayout(got, layout) {
				t.Fatalf("spec #%d: Unrank(Rank(%v)) = %v", idx, layout.Start, got.Start)
			}
		}
		if total > 0 && uint64(len(seen)) != total {
			t.Fatalf("spec #%d: ranks do not cover [0,%d)", idx, total)
		}
	}
}

func TestUnrankOutOfRange(t *testing.T) {
	spec := Spec{DomainPages: 4, Regions: []Region{{SizePages: 1, AlignPages: 1}}}
	total, err := Count(spec)
	if err != nil || total == 0 {
		t.Fatalf("unexpected Count: %d %v", total, err)
	}
	if _, err := Unrank(spec, total); !errors.Is(err, ErrInvalidRank) {
		t.Fatalf("want ErrInvalidRank, got %v", err)
	}
	if _, err := Rank(spec, Layout{Start: []int{99}}); !errors.Is(err, ErrInvalidRank) {
		t.Fatalf("want ErrInvalidRank, got %v", err)
	}
	if _, err := Rank(spec, Layout{Start: []int{0, 1}}); !errors.Is(err, ErrInvalidRank) {
		t.Fatalf("want ErrInvalidRank, got %v", err)
	}
}

func TestNoLayoutZeroSolution(t *testing.T) {
	// 保留页完全占满域，区域无处可放。
	blocked := Spec{
		DomainPages: 4,
		Regions:     []Region{{SizePages: 2, AlignPages: 1}},
		Reserved:    []Reservation{{StartPage: 0, Pages: 4}},
	}
	total, err := Count(blocked)
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if total != 0 {
		t.Fatalf("want 0 layouts, got %d", total)
	}
	if _, err := Unrank(blocked, 0); !errors.Is(err, ErrNoLayout) {
		t.Fatalf("want ErrNoLayout, got %v", err)
	}

	// 零解时抽样不得消耗任何随机字节。
	stream := NewStream(bytes.NewReader(make([]byte, 16)))
	if _, err := Sample(context.Background(), blocked, stream); !errors.Is(err, ErrNoLayout) {
		t.Fatalf("want ErrNoLayout, got %v", err)
	}
	if pos := stream.Position(); pos != 0 {
		t.Fatalf("zero-solution sample consumed %d bytes, want 0", pos)
	}

	// 对齐导致无解：需要 2 对齐，但仅剩的可用页是奇数页。
	odd := Spec{
		DomainPages: 4,
		Regions:     []Region{{SizePages: 2, AlignPages: 2}},
		Reserved:    []Reservation{{StartPage: 0, Pages: 1}, {StartPage: 3, Pages: 1}},
	}
	total, err = Count(odd)
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if total != 0 {
		t.Fatalf("want 0 alignment-blocked layouts, got %d", total)
	}
}

func TestSampleUniformityAcrossFullDistribution(t *testing.T) {
	spec := Spec{
		DomainPages: 6,
		Regions: []Region{
			{SizePages: 2, AlignPages: 2},
			{SizePages: 1, AlignPages: 1},
		},
		Reserved: []Reservation{{StartPage: 4, Pages: 1}},
	}
	total, err := Count(spec)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total < 2 {
		t.Fatalf("test needs multiple layouts, got %d", total)
	}

	const draws = 60000
	counts := make([]int, total)
	stream := NewStream(newLCG(0x9e3779b97f4a7c15))
	for i := 0; i < draws; i++ {
		layout, err := Sample(context.Background(), spec, stream)
		if err != nil {
			t.Fatalf("Sample: %v", err)
		}
		rank, err := Rank(spec, layout)
		if err != nil {
			t.Fatalf("Rank: %v", err)
		}
		counts[rank]++
	}
	assertChiSquare(t, counts, float64(draws)/float64(total))
}

func TestDrawUniformNoModuloBias(t *testing.T) {
	const n = uint64(7) // 非 2 的幂，朴素取模会产生偏置
	const draws = 140000
	counts := make([]int, n)
	stream := NewStream(newLCG(1234567))
	for i := 0; i < draws; i++ {
		value, err := DrawUniform(stream, n)
		if err != nil {
			t.Fatalf("DrawUniform: %v", err)
		}
		counts[value]++
	}
	assertChiSquare(t, counts, float64(draws)/float64(n))
}

func TestDrawUniformSingleValueConsumesNothing(t *testing.T) {
	stream := NewStream(bytes.NewReader([]byte{0xff}))
	value, err := DrawUniform(stream, 1)
	if err != nil || value != 0 {
		t.Fatalf("DrawUniform(1) = %d, %v", value, err)
	}
	if pos := stream.Position(); pos != 0 {
		t.Fatalf("n=1 consumed %d bytes, want 0", pos)
	}
}

func TestStreamPositionOnExhaustion(t *testing.T) {
	stream := NewStream(bytes.NewReader([]byte{0x00}))
	// n=300 需要 9 位即两个字节；只提供一个，第二个字节处返回 EOF，位置停在 1。
	if _, err := DrawUniform(stream, 300); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
	if pos := stream.Position(); pos != 1 {
		t.Fatalf("position after EOF = %d, want 1", pos)
	}
}

func TestCancellationPreservesStreamPosition(t *testing.T) {
	spec := Spec{DomainPages: 5, Regions: []Region{{SizePages: 1, AlignPages: 1}}}

	// 已取消的上下文：抽样必须在读取任何随机字节之前中止。
	stream := NewStream(newLCG(1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sample(ctx, spec, stream); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if pos := stream.Position(); pos != 0 {
		t.Fatalf("canceled sample consumed %d bytes, want 0", pos)
	}

	// 读取过程中触发取消：失败后位置精确等于已读字节数。
	ctx2, cancel2 := context.WithCancel(context.Background())
	failing := &cancelAfterReader{after: 1, cancel: cancel2}
	stream2 := NewStream(failing)
	if _, err := Sample(ctx2, spec, stream2); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled mid-draw, got %v", err)
	}
	if pos := stream2.Position(); pos != 1 {
		t.Fatalf("position after mid-draw cancellation = %d, want 1", pos)
	}

	// 相同字节前缀产生相同布局与相同流位置，证明随机流可精确接续。
	layout1, err := Sample(context.Background(), spec, NewStream(newLCG(42)))
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	layout2, err := Sample(context.Background(), spec, NewStream(newLCG(42)))
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if !sameLayout(layout1, layout2) {
		t.Fatalf("deterministic streams diverged: %v vs %v", layout1.Start, layout2.Start)
	}
}

func TestInvalidSpecs(t *testing.T) {
	cases := []Spec{
		{DomainPages: 0},
		{DomainPages: MaxDomainPages + 1},
		{DomainPages: 4, Regions: make([]Region, MaxRegions+1)},
		{DomainPages: 4, Regions: []Region{{SizePages: 0, AlignPages: 1}}},
		{DomainPages: 4, Regions: []Region{{SizePages: 1, AlignPages: 3}}},
		{DomainPages: 4, Regions: []Region{{SizePages: 5, AlignPages: 1}}},
		{DomainPages: 4, Reserved: []Reservation{{StartPage: 2, Pages: 99}}},
	}
	for idx, spec := range cases {
		if _, err := Count(spec); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("case %d: want ErrInvalidSpec, got %v", idx, err)
		}
	}
}

func TestEmptySpecSingleEmptyLayout(t *testing.T) {
	spec := Spec{DomainPages: 2}
	total, err := Count(spec)
	if err != nil || total != 1 {
		t.Fatalf("Count = %d, %v; want 1", total, err)
	}
	layout, err := Unrank(spec, 0)
	if err != nil || len(layout.Start) != 0 {
		t.Fatalf("Unrank empty spec: %v %v", layout, err)
	}
	rank, err := Rank(spec, Layout{})
	if err != nil || rank != 0 {
		t.Fatalf("Rank empty = %d, %v", rank, err)
	}
	stream := NewStream(bytes.NewReader(make([]byte, 4)))
	if _, err := Sample(context.Background(), spec, stream); err != nil {
		t.Fatalf("Sample empty: %v", err)
	}
	if pos := stream.Position(); pos != 0 {
		t.Fatalf("empty layout consumed %d bytes, want 0", pos)
	}
}
