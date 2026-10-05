package main

import (
	"math/rand"
	"testing"
)

func TestSharedEdgesNoDoubleCount(t *testing.T) {
	// 两个共边矩形：并集面积必须等于面积之和，共边不重复计数
	layers := [][]Rect{{
		{0, 0, 10, 10},
		{10, 0, 20, 10}, // 与上一块共边 x=10
		{0, 10, 20, 20}, // 与上两块共边 y=10
	}}
	sum, union, ok := CheckConservation(layers)
	if !ok || sum != 400 || union != 400 {
		t.Fatalf("sum=%d union=%d ok=%v, want 400/400/true", sum, union, ok)
	}
	if got := TopmostAt(layers, 10, 10); got != 0 {
		t.Fatalf("corner point should belong to layer 0 (half-open), got %d", got)
	}
}

func TestFullOcclusion(t *testing.T) {
	layers := [][]Rect{
		{{0, 0, 100, 100}},
		{{-50, -50, 150, 150}}, // 完全遮挡层 0
	}
	areas, visible := VisibleAreas(layers)
	if areas[0] != 0 || len(visible[0]) != 0 {
		t.Fatalf("layer 0 should be fully occluded, got area=%d parts=%v", areas[0], visible[0])
	}
	if areas[1] != 40000 {
		t.Fatalf("layer 1 visible area = %d, want 40000", areas[1])
	}
}

func TestHoleOverlap(t *testing.T) {
	// 两层孔洞错位重叠：交集处透视到最底层
	layers := [][]Rect{
		{{0, 0, 10, 10}}, // 层 0 实体
		NewLayerWithHoles(Rect{0, 0, 10, 10}, []Rect{{2, 2, 8, 8}}), // 层 1 孔 [2,8)²
		NewLayerWithHoles(Rect{0, 0, 10, 10}, []Rect{{5, 5, 9, 9}}), // 层 2 孔 [5,9)²
	}
	areas, _ := VisibleAreas(layers)
	// 层 2 可见: 100-16=84；层 1 仅从层 2 孔洞露出: 16-9=7；层 0 仅从孔洞交集 [5,8)² 露出: 9
	want := []int64{9, 7, 84}
	for i := range want {
		if areas[i] != want[i] {
			t.Fatalf("layer %d visible = %d, want %d", i, areas[i], want[i])
		}
	}
	if got := TopmostAt(layers, 6, 6); got != 0 { // 两孔交集
		t.Fatalf("(6,6) should see layer 0, got %d", got)
	}
	if got := TopmostAt(layers, 8, 8); got != 1 { // 层 2 孔内、层 1 实体（半开边界）
		t.Fatalf("(8,8) should see layer 1, got %d", got)
	}
	if got := TopmostAt(layers, 3, 3); got != 2 { // 层 1 孔内、层 2 实体
		t.Fatalf("(3,3) should see layer 2, got %d", got)
	}
}

func TestConservationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 1 + rng.Intn(6)
		layers := make([][]Rect, n)
		for i := range layers {
			var rects []Rect
			for k := 0; k < 1+rng.Intn(4); k++ {
				x1 := int64(rng.Intn(1000))
				y1 := int64(rng.Intn(1000))
				rects = append(rects, Rect{x1, y1, x1 + int64(rng.Intn(500)), y1 + int64(rng.Intn(500))})
			}
			layers[i] = rects
		}
		sum, union, ok := CheckConservation(layers)
		if !ok {
			t.Fatalf("trial %d: conservation violated: sum=%d union=%d", trial, sum, union)
		}
	}
}

func TestLayerMapMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	layers := make([][]Rect, 4)
	for i := range layers {
		var rects []Rect
		for k := 0; k < 3; k++ {
			x1 := int64(rng.Intn(1_000_000_000))
			y1 := int64(rng.Intn(1_000_000_000))
			rects = append(rects, Rect{x1, y1, x1 + int64(rng.Intn(500_000_000)), y1 + int64(rng.Intn(500_000_000))})
		}
		layers[i] = rects
	}
	lm := NewLayerMap(layers)
	for q := 0; q < 2000; q++ {
		x := int64(rng.Intn(1_600_000_000)) - 50_000_000
		y := int64(rng.Intn(1_600_000_000)) - 50_000_000
		if got, want := lm.TopmostAt(x, y), TopmostAt(layers, x, y); got != want {
			t.Fatalf("point (%d,%d): map=%d brute=%d", x, y, got, want)
		}
	}
	// 边界点也要一致（半开语义）
	for _, r := range layers[2] {
		for _, p := range [][2]int64{{r.X1, r.Y1}, {r.X2, r.Y2}, {r.X2 - 1, r.Y2 - 1}} {
			if got, want := lm.TopmostAt(p[0], p[1]), TopmostAt(layers, p[0], p[1]); got != want {
				t.Fatalf("edge point %v: map=%d brute=%d", p, got, want)
			}
		}
	}
}
