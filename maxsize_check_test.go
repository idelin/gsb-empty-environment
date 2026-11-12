package netsec

import (
	"context"
	"testing"
)

func TestMaxSizeCountRankAndSample(t *testing.T) {
	spec := Spec{
		DomainPages: MaxDomainPages,
		Regions: []Region{
			{SizePages: 2, AlignPages: 4},
			{SizePages: 3, AlignPages: 1},
			{SizePages: 1, AlignPages: 8},
			{SizePages: 4, AlignPages: 2},
			{SizePages: 2, AlignPages: 1},
			{SizePages: 1, AlignPages: 16},
		},
		Reserved: []Reservation{{StartPage: 10, Pages: 2}, {StartPage: 25, Pages: 1}},
	}
	total, err := Count(spec)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total == 0 {
		t.Fatal("expected nonzero layouts at max size")
	}
	// 全部排名都必须能逆排名并回到自身。
	for rank := uint64(0); rank < total; rank++ {
		layout, err := Unrank(spec, rank)
		if err != nil {
			t.Fatalf("Unrank(%d): %v", rank, err)
		}
		back, err := Rank(spec, layout)
		if err != nil || back != rank {
			t.Fatalf("rank roundtrip %d -> %d, err %v", rank, back, err)
		}
	}
	stream := NewStream(newLCG(99))
	if _, err := Sample(context.Background(), spec, stream); err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if stream.Position() == 0 {
		t.Fatal("expected nonzero random consumption")
	}
}
