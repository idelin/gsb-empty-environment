package taskpkg

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestTinyMipSafety(t *testing.T) {
	rects := []Rect{
		{ID: 3, Width: 5, Height: 3},
		{ID: 1, Width: 1, Height: 1},
		{ID: 2, Width: 7, Height: 2},
	}
	a, err := Plan(rects, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Alignment != 16 || a.Border != 16 {
		t.Fatalf("alignment=%d border=%d, want 16/16", a.Alignment, a.Border)
	}

	for _, p := range a.Placements {
		input := rects[indexForID(rects, p.ID)]
		wantW, wantH := input.Width, input.Height
		if p.Rotated {
			wantW, wantH = wantH, wantW
		}
		if p.Content.Width != wantW || p.Content.Height != wantH {
			t.Errorf("id %d content=%dx%d, want %dx%d", p.ID, p.Content.Width, p.Content.Height, wantW, wantH)
		}
		for k, m := range p.Mips {
			scale := 1 << uint(k)
			if m.Border != a.Alignment/scale {
				t.Errorf("id %d level %d border=%d, want %d", p.ID, k, m.Border, a.Alignment/scale)
			}
			if m.Border < 1 {
				t.Errorf("id %d level %d has no border", p.ID, k)
			}
			if m.Valid.Width < 1 || m.Valid.Height < 1 {
				t.Errorf("id %d level %d has empty valid region", p.ID, k)
			}
			if m.Content.X < m.Valid.X || m.Content.Y < m.Valid.Y ||
				m.Content.Right() > m.Valid.Right() || m.Content.Bottom() > m.Valid.Bottom() {
				t.Errorf("id %d level %d content escapes valid region", p.ID, k)
			}
			if m.Valid.X-m.Expanded.X != m.Border ||
				m.Valid.Y-m.Expanded.Y != m.Border ||
				m.Expanded.Right()-m.Valid.Right() != m.Border ||
				m.Expanded.Bottom()-m.Valid.Bottom() != m.Border {
				t.Errorf("id %d level %d border is not uniform", p.ID, k)
			}
			// Exact correspondence with level zero.
			if m.Valid.X != p.Valid.X/scale || m.Valid.Y != p.Valid.Y/scale ||
				m.Valid.Width != p.Valid.Width/scale || m.Valid.Height != p.Valid.Height/scale {
				t.Errorf("id %d level %d valid region does not correspond to level zero", p.ID, k)
			}
		}
	}
}

func TestSmallExhaustiveAgainstFullSearch(t *testing.T) {
	dimensions := []struct{ w, h int }{
		{1, 1}, {1, 2}, {2, 1}, {2, 2}, {2, 3}, {3, 2}, {3, 3},
	}

	checked := 0
	for n := 1; n <= 3; n++ {
		var walk func(idx int, chosen []struct{ w, h int })
		walk = func(idx int, chosen []struct{ w, h int }) {
			if idx == n {
				for maxMip := 0; maxMip <= 2; maxMip++ {
					rects := make([]Rect, n)
					for i, d := range chosen {
						rects[i] = Rect{ID: 10 - i, Width: d.w, Height: d.h}
					}
					atlas, err := Plan(rects, maxMip)
					if err != nil {
						t.Fatalf("n=%d maxMip=%d dims=%v: %v", n, maxMip, chosen, err)
					}
					if err := atlas.Validate(); err != nil {
						t.Fatalf("n=%d maxMip=%d dims=%v: %v", n, maxMip, chosen, err)
					}
					if !isPowerOfTwo(atlas.Width) || !isPowerOfTwo(atlas.Height) {
						t.Fatalf("non-power-of-two atlas %dx%d", atlas.Width, atlas.Height)
					}

					align := 1 << uint(maxMip)
					wantArea, wantLong := bruteMinMetrics(rects, maxMip)
					// bruteMinMetrics works in alignment granules.
					gotArea := int64(atlas.Width/align) * int64(atlas.Height/align)
					gotLong := int64(maxInt(atlas.Width/align, atlas.Height/align))
					if gotArea != wantArea || gotLong != wantLong {
						t.Fatalf("n=%d maxMip=%d dims=%v: got %d x %d (area %d long %d), brute force wants area %d long %d",
							n, maxMip, chosen, atlas.Width, atlas.Height, gotArea, gotLong, wantArea, wantLong)
					}
					if atlas.Alignment != align {
						t.Fatalf("alignment=%d, want %d", atlas.Alignment, align)
					}
					for _, p := range atlas.Placements {
						for k, m := range p.Mips {
							if m.Border < 1 {
								t.Fatalf("id=%d level=%d lost its border", p.ID, k)
							}
						}
					}
					checked++
				}
				return
			}
			for _, d := range dimensions {
				walk(idx+1, append(chosen, d))
			}
		}
		walk(0, nil)
	}
	t.Logf("checked %d small atlases against independent full search", checked)
}

func TestInputOrderDoesNotChangePlan(t *testing.T) {
	rects := []Rect{
		{ID: 7, Width: 3, Height: 11},
		{ID: 2, Width: 9, Height: 4},
		{ID: 5, Width: 1, Height: 6},
		{ID: 9, Width: 5, Height: 5},
	}
	a, err := Plan(rects, 2)
	if err != nil {
		t.Fatal(err)
	}
	reversed := make([]Rect, len(rects))
	for i := range rects {
		reversed[i] = rects[len(rects)-1-i]
	}
	b, err := Plan(reversed, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Width != b.Width || a.Height != b.Height {
		t.Fatalf("input order changed atlas size: %dx%d vs %dx%d", a.Width, a.Height, b.Width, b.Height)
	}
	for _, ra := range rects {
		pa, _ := a.PlacementByID(ra.ID)
		pb, _ := b.PlacementByID(ra.ID)
		if pa.Expanded != pb.Expanded || pa.Rotated != pb.Rotated {
			t.Fatalf("input order changed ID %d: %+v vs %+v", ra.ID, pa.Expanded, pb.Expanded)
		}
	}
}

func TestRandomSmallAgainstFullSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		n := 4
		maxMip := rng.Intn(3)
		rects := make([]Rect, n)
		for i := range rects {
			rects[i] = Rect{
				ID:     1000 - i,
				Width:  1 + rng.Intn(6),
				Height: 1 + rng.Intn(6),
			}
		}
		atlas, err := Plan(rects, maxMip)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := atlas.Validate(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		align := 1 << uint(maxMip)
		wantArea, wantLong := bruteMinMetrics(rects, maxMip)
		gotArea := int64(atlas.Width/align) * int64(atlas.Height/align)
		gotLong := int64(maxInt(atlas.Width/align, atlas.Height/align))
		if gotArea != wantArea || gotLong != wantLong {
			t.Fatalf("trial %d rects=%v maxMip=%d: got area/long %d/%d, want %d/%d",
				trial, rects, maxMip, gotArea, gotLong, wantArea, wantLong)
		}
	}
}

func TestPositionVectorTieBreak(t *testing.T) {
	cases := []struct {
		items  []packItem
		width  int64
		height int64
	}{
		{
			items: []packItem{
				{index: 0, id: 9, expW: 3, expH: 3},
				{index: 1, id: 2, expW: 3, expH: 3},
				{index: 2, id: 5, expW: 2, expH: 4},
			},
			width: 6, height: 8,
		},
		{
			items: []packItem{
				{index: 0, id: 4, expW: 2, expH: 5},
				{index: 1, id: 1, expW: 4, expH: 3},
				{index: 2, id: 8, expW: 3, expH: 3},
				{index: 3, id: 2, expW: 2, expH: 2},
			},
			width: 8, height: 8,
		},
		{
			items: []packItem{
				{index: 0, id: 7, expW: 3, expH: 3},
				{index: 1, id: 3, expW: 3, expH: 3},
				{index: 2, id: 6, expW: 4, expH: 2},
				{index: 3, id: 1, expW: 2, expH: 4},
			},
			width: 8, height: 6,
		},
	}
	for ci, tc := range cases {
		seed, ok := packAny(tc.items, tc.width, tc.height)
		if !ok {
			t.Fatalf("case %d is unexpectedly infeasible", ci)
		}
		got, ok := packBest(tc.items, tc.width, tc.height, seed)
		if !ok {
			t.Fatalf("case %d: packBest failed", ci)
		}
		want, ok := bruteLexPositions(tc.items, int(tc.width), int(tc.height))
		if !ok {
			t.Fatalf("case %d: brute lexicographic solver failed", ci)
		}
		if !packEqual(tc.items, got, want) {
			t.Fatalf("case %d: got position vector %v, want %v", ci, got, want)
		}
	}
}

func TestTwelveRects(t *testing.T) {
	rects := []Rect{
		{ID: 0, Width: 1, Height: 17},
		{ID: 1, Width: 3, Height: 14},
		{ID: 2, Width: 5, Height: 11},
		{ID: 3, Width: 7, Height: 9},
		{ID: 4, Width: 11, Height: 7},
		{ID: 5, Width: 13, Height: 5},
		{ID: 6, Width: 17, Height: 3},
		{ID: 7, Width: 19, Height: 2},
		{ID: 8, Width: 23, Height: 1},
		{ID: 9, Width: 29, Height: 31},
		{ID: 10, Width: 100, Height: 2},
		{ID: 11, Width: 1, Height: 1},
	}
	for _, maxMip := range []int{0, 2, 5} {
		a, err := Plan(rects, maxMip)
		if err != nil {
			t.Fatalf("maxMip %d: %v", maxMip, err)
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("maxMip %d: %v", maxMip, err)
		}
		if len(a.Placements) != len(rects) {
			t.Fatalf("maxMip %d: got %d placements", maxMip, len(a.Placements))
		}
		for _, r := range rects {
			if _, ok := a.PlacementByID(r.ID); !ok {
				t.Fatalf("maxMip %d: missing ID %d", maxMip, r.ID)
			}
		}
	}
}

func TestPlanErrors(t *testing.T) {
	if _, err := Plan(nil, 0); err == nil {
		t.Fatal("empty input should fail")
	}
	tooMany := make([]Rect, MaxRects+1)
	for i := range tooMany {
		tooMany[i] = Rect{ID: i, Width: 1, Height: 1}
	}
	if _, err := Plan(tooMany, 0); err == nil {
		t.Fatal("more than MaxRects should fail")
	}
	if _, err := Plan([]Rect{{ID: 1, Width: 0, Height: 1}}, 0); err == nil {
		t.Fatal("zero width should fail")
	}
	if _, err := Plan([]Rect{{ID: 1, Width: 1, Height: 1}, {ID: 1, Width: 1, Height: 1}}, 0); err == nil {
		t.Fatal("duplicate ID should fail")
	}
	if _, err := Plan([]Rect{{ID: 1, Width: 1, Height: 1}}, -1); err == nil {
		t.Fatal("negative mip level should fail")
	}

	a, err := PlanMipLevels([]Rect{{ID: 1, Width: 1, Height: 1}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if a.MaxMipLevel != 2 || a.MipLevels != 3 || a.Alignment != 4 {
		t.Fatalf("PlanMipLevels(3) produced max=%d levels=%d alignment=%d", a.MaxMipLevel, a.MipLevels, a.Alignment)
	}
}

func indexForID(rects []Rect, id int) int {
	for i := range rects {
		if rects[i].ID == id {
			return i
		}
	}
	panic(fmt.Sprintf("missing ID %d", id))
}

// bruteMinMetrics is an independent implementation used only by tests. It
// works on the aligned granule grid, tries every power-of-two shape, and uses
// a full grid search for packability.
func bruteMinMetrics(rects []Rect, maxMip int) (area, longSide int64) {
	align := 1 << uint(maxMip)
	items := make([]bruteItem, len(rects))
	var sumW, sumH int64
	for i, r := range rects {
		vw := int64(maxInt(ceilDiv(r.Width, align), 1))
		vh := int64(maxInt(ceilDiv(r.Height, align), 1))
		items[i] = bruteItem{w: vw + 2, h: vh + 2}
		sumW += items[i].w
		sumH += items[i].h
	}
	upper := nextPow2(sumW)
	if p := nextPow2(sumH); p > upper {
		upper = p
	}
	type candidate struct {
		w, h, area, long int64
	}
	var candidates []candidate
	limitArea := upper * upper
	for _, w := range powersUpTo(limitArea) {
		for _, h := range powersUpTo(limitArea) {
			if h > limitArea/w {
				continue
			}
			if w*int64(h) < totalArea(items) {
				continue
			}
			candidates = append(candidates, candidate{w: w, h: h, area: w * h, long: maxInt64(w, h)})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].area != candidates[j].area {
			return candidates[i].area < candidates[j].area
		}
		if candidates[i].long != candidates[j].long {
			return candidates[i].long < candidates[j].long
		}
		return candidates[i].w < candidates[j].w
	})
	for start := 0; start < len(candidates); {
		end := start
		for end < len(candidates) && candidates[end].area == candidates[start].area &&
			candidates[end].long == candidates[start].long {
			end++
		}
		for _, c := range candidates[start:end] {
			if bruteCanPack(items, int(c.w), int(c.h)) {
				return c.area, c.long
			}
		}
		start = end
	}
	return -1, -1
}

type bruteItem struct{ w, h int64 }

func totalArea(items []bruteItem) int64 {
	var total int64
	for _, item := range items {
		total += item.w * item.h
	}
	return total
}

func bruteLexPositions(items []packItem, width, height int) ([]packPos, bool) {
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		if items[order[i]].id != items[order[j]].id {
			return items[order[i]].id < items[order[j]].id
		}
		return items[order[i]].index < items[order[j]].index
	})
	cells := make([]bool, width*height)
	out := make([]packPos, len(items))
	var rec func(depth int) bool
	rec = func(depth int) bool {
		if depth == len(order) {
			return true
		}
		item := items[order[depth]]
		for x := 0; x < width; x++ {
			for y := 0; y < height; y++ {
				for rotation := 0; rotation < 2; rotation++ {
					w, h := int(item.expW), int(item.expH)
					if rotation == 1 {
						if w == h {
							continue
						}
						w, h = h, w
					}
					if x+w > width || y+h > height {
						continue
					}
					ok := true
					for yy := 0; yy < h && ok; yy++ {
						for xx := 0; xx < w; xx++ {
							if cells[(y+yy)*width+(x+xx)] {
								ok = false
								break
							}
						}
					}
					if !ok {
						continue
					}
					for yy := 0; yy < h; yy++ {
						for xx := 0; xx < w; xx++ {
							cells[(y+yy)*width+(x+xx)] = true
						}
					}
					out[item.index] = packPos{x: int64(x), y: int64(y), rotated: rotation == 1}
					if rec(depth + 1) {
						return true
					}
					for yy := 0; yy < h; yy++ {
						for xx := 0; xx < w; xx++ {
							cells[(y+yy)*width+(x+xx)] = false
						}
					}
				}
			}
		}
		return false
	}
	return out, rec(0)
}

func packEqual(items []packItem, a, b []packPos) bool {
	return !packVectorLess(items, a, b) && !packVectorLess(items, b, a)
}

func bruteCanPack(items []bruteItem, width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	// Place the largest items first; positions are still exhaustive, so this
	// only changes search order.
	ordered := append([]bruteItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].w*ordered[i].h > ordered[j].w*ordered[j].h })
	cells := make([]bool, width*height)
	var rec func(index int) bool
	rec = func(index int) bool {
		if index == len(ordered) {
			return true
		}
		item := ordered[index]
		for rotation := 0; rotation < 2; rotation++ {
			if rotation == 1 && item.w == item.h {
				continue
			}
			o := [2]int{int(item.w), int(item.h)}
			if rotation == 1 {
				o = [2]int{int(item.h), int(item.w)}
			}
			for y := 0; y+o[1] <= height; y++ {
				for x := 0; x+o[0] <= width; x++ {
					ok := true
					for yy := 0; yy < o[1] && ok; yy++ {
						for xx := 0; xx < o[0]; xx++ {
							if cells[(y+yy)*width+(x+xx)] {
								ok = false
								break
							}
						}
					}
					if !ok {
						continue
					}
					for yy := 0; yy < o[1]; yy++ {
						for xx := 0; xx < o[0]; xx++ {
							cells[(y+yy)*width+(x+xx)] = true
						}
					}
					if rec(index + 1) {
						return true
					}
					for yy := 0; yy < o[1]; yy++ {
						for xx := 0; xx < o[0]; xx++ {
							cells[(y+yy)*width+(x+xx)] = false
						}
					}
				}
			}
		}
		return false
	}
	return rec(0)
}
