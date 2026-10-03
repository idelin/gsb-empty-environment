package broadphase

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

func randomBox(rng *rand.Rand, extent float64) AABB {
	cx, cy := rng.Float64()*extent, rng.Float64()*extent
	hx, hy := 0.1+rng.Float64()*1.9, 0.1+rng.Float64()*1.9
	return AABB{Min: Vec2{cx - hx, cy - hy}, Max: Vec2{cx + hx, cy + hy}}
}

func bruteQuery(boxes map[int]AABB, region AABB) map[int]bool {
	got := make(map[int]bool)
	for id, b := range boxes {
		if b.Overlaps(region) {
			got[id] = true
		}
	}
	return got
}

func brutePairs(boxes map[int]AABB) map[Pair]bool {
	ids := make([]int, 0, len(boxes))
	for id := range boxes {
		ids = append(ids, id)
	}
	got := make(map[Pair]bool)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if boxes[ids[i]].Overlaps(boxes[ids[j]]) {
				a, b := ids[i], ids[j]
				if a > b {
					a, b = b, a
				}
				got[Pair{a, b}] = true
			}
		}
	}
	return got
}

// brutePairsSorted 以 O(N^2) 暴力法产出排序后的配对列表，用于与 UpdatePairs 对比。
func brutePairsSorted(boxes map[int]AABB) []Pair {
	ids := make([]int, 0, len(boxes))
	for id := range boxes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var pairs []Pair
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if boxes[ids[i]].Overlaps(boxes[ids[j]]) {
				pairs = append(pairs, Pair{ids[i], ids[j]})
			}
		}
	}
	return pairs
}

// 树插入/删除/区域查询与暴力结果一致，且树高保持 O(log N)。
func TestTreeInsertRemoveQuery(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tree := NewTree()
	boxes := make(map[int]AABB)

	for i := 0; i < 300; i++ {
		b := randomBox(rng, 100)
		boxes[i] = b
		tree.Insert(i, b)
	}
	if tree.Len() != 300 {
		t.Fatalf("Len = %d, want 300", tree.Len())
	}

	checkQueries := func() {
		for q := 0; q < 50; q++ {
			region := randomBox(rng, 100)
			got := make(map[int]bool)
			tree.Query(region, func(id int) bool { got[id] = true; return true })
			want := bruteQuery(boxes, region)
			if fmt.Sprint(got) != fmt.Sprint(want) && !equalSet(got, want) {
				t.Fatalf("query mismatch: got %d ids, want %d", len(got), len(want))
			}
		}
	}
	checkQueries()

	// 删除一半后重新校验。
	for i := 0; i < 300; i += 2 {
		tree.Remove(i)
		delete(boxes, i)
	}
	checkQueries()

	if h := tree.Height(); h < 0 || h > 30 {
		t.Fatalf("tree height %d out of expected O(log N) range", h)
	}
}

func equalSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// 树内自相交遍历产出的叶子对与暴力 O(N^2) 结果完全一致。
func TestCollidePairsMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	tree := NewTree()
	boxes := make(map[int]AABB)
	for i := 0; i < 300; i++ {
		b := randomBox(rng, 100)
		boxes[i] = b
		tree.Insert(i, b)
	}

	check := func(stage string) {
		got := make(map[Pair]bool)
		tree.CollidePairs(func(a, b int) {
			if a > b {
				a, b = b, a
			}
			p := Pair{a, b}
			if got[p] {
				t.Fatalf("%s: pair %v emitted twice", stage, p)
			}
			got[p] = true
		})
		want := brutePairs(boxes)
		if len(got) != len(want) {
			t.Fatalf("%s: got %d pairs, want %d", stage, len(got), len(want))
		}
		for p := range want {
			if !got[p] {
				t.Fatalf("%s: missing pair %v", stage, p)
			}
		}
	}
	check("initial")

	for i := 0; i < 300; i += 3 {
		tree.Remove(i)
		delete(boxes, i)
	}
	check("after removals")
}

// 验收场景：500 个刚体同时运动，恰好 12 对包围盒重叠。
func buildAcceptanceScene(bp *Broadphase) ([]AABB, []Vec2) {
	const n = 500
	boxes := make([]AABB, n)
	vels := make([]Vec2, n)
	rng := rand.New(rand.NewSource(7))

	boxAt := func(x, y float64) AABB {
		return AABB{Min: Vec2{x - 0.5, y - 0.5}, Max: Vec2{x + 0.5, y + 0.5}}
	}
	// 网格散布，间距 10，互不重叠。
	const cols = 23
	for i := 0; i < n; i++ {
		x := float64(i%cols) * 10
		y := float64(i/cols) * 10
		boxes[i] = boxAt(x, y)
		vels[i] = Vec2{rng.Float64()*0.04 - 0.02, rng.Float64()*0.04 - 0.02}
	}
	// 埋入 12 对重叠刚体，彼此同速运动以保持重叠。
	for p := 0; p < 12; p++ {
		a, b := 2*p, 2*p+1
		base := Vec2{-1000 + float64(p)*20, -1000}
		v := Vec2{0.03, -0.02}
		boxes[a], vels[a] = boxAt(base.X, base.Y), v
		boxes[b], vels[b] = boxAt(base.X+0.5, base.Y), v
	}
	for i := range boxes {
		bp.Add(i, boxes[i])
	}
	return boxes, vels
}

func moveBox(b AABB, d Vec2) AABB {
	return AABB{Min: Vec2{b.Min.X + d.X, b.Min.Y + d.Y}, Max: Vec2{b.Max.X + d.X, b.Max.Y + d.Y}}
}

func TestAcceptance500Bodies12Pairs(t *testing.T) {
	bp := New(0.1, 2.0)
	boxes, vels := buildAcceptanceScene(bp)

	pairs := bp.UpdatePairs()
	if len(pairs) != 12 {
		t.Fatalf("got %d pairs, want 12: %v", len(pairs), pairs)
	}
	// 恰好是埋入的 12 对。
	for p := 0; p < 12; p++ {
		want := Pair{2 * p, 2*p + 1}
		if pairs[p] != want {
			t.Fatalf("pairs[%d] = %v, want %v", p, pairs[p], want)
		}
	}

	// 模拟 120 帧同时运动，每帧结果都与暴力法一致。
	for f := 0; f < 120; f++ {
		for i := range boxes {
			boxes[i] = moveBox(boxes[i], vels[i])
			bp.Move(i, boxes[i], vels[i])
		}
		pairs := bp.UpdatePairs()

		fat := make(map[int]AABB, len(boxes))
		for i := range boxes {
			fat[i] = bp.FatAABB(i)
		}
		want := brutePairsSorted(fat)
		if !slices.Equal(pairs, want) {
			t.Fatalf("frame %d: pairs mismatch: got %d, want %d", f, len(pairs), len(want))
		}
	}
}

// 位移未超出肥胖边距时不触发树更新；超出后增量重插。
func TestMoveWithinFatMarginSkipsTreeUpdate(t *testing.T) {
	bp := New(0.1, 0)
	box := AABB{Min: Vec2{0, 0}, Max: Vec2{1, 1}}
	bp.Add(1, box)
	bp.Add(2, AABB{Min: Vec2{10, 10}, Max: Vec2{11, 11}})
	fatBefore := bp.FatAABB(1)

	// 移动 0.05 < margin 0.1：树中包围盒不变。
	bp.Move(1, moveBox(box, Vec2{0.05, 0}), Vec2{0.05, 0})
	if bp.FatAABB(1) != fatBefore {
		t.Fatal("small move should not update the tree")
	}

	// 移动 0.5 > margin：触发重插，肥胖包围盒跟随新位置。
	bp.Move(1, moveBox(box, Vec2{0.5, 0}), Vec2{0.5, 0})
	if bp.FatAABB(1) == fatBefore {
		t.Fatal("large move should reinsert the proxy")
	}
	if !bp.FatAABB(1).Contains(moveBox(box, Vec2{0.5, 0})) {
		t.Fatal("fat AABB should contain the new tight box")
	}
}

// 树未变化时 UpdatePairs 返回缓存结果。
func TestUpdatePairsCachedWhenClean(t *testing.T) {
	bp := New(0.1, 0)
	bp.Add(1, AABB{Min: Vec2{0, 0}, Max: Vec2{1, 1}})
	bp.Add(2, AABB{Min: Vec2{0.5, 0}, Max: Vec2{1.5, 1}})
	first := bp.UpdatePairs()
	second := bp.UpdatePairs()
	if len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatal("expected cached pairs when tree is clean")
	}
}

func TestEdgeCases(t *testing.T) {
	bp := New(0.1, 0)
	if got := bp.UpdatePairs(); len(got) != 0 {
		t.Fatalf("empty scene: got %d pairs", len(got))
	}
	bp.Add(1, AABB{Min: Vec2{0, 0}, Max: Vec2{1, 1}})
	if got := bp.UpdatePairs(); len(got) != 0 {
		t.Fatalf("single body: got %d pairs", len(got))
	}
	bp.Remove(1)
	if got := bp.UpdatePairs(); len(got) != 0 {
		t.Fatalf("after removal: got %d pairs", len(got))
	}
}

// 基准：500 刚体同时运动一整帧的粗测耗时（移动更新 + 自相交遍历）。
func BenchmarkBroadphase500(b *testing.B) {
	bp := New(0.1, 2.0)
	boxes, vels := buildAcceptanceScene(bp)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 每 60 帧反向，避免长期漂移导致网格刚体相撞。
		dir := 1.0
		if (i/60)%2 == 1 {
			dir = -1
		}
		for j := range boxes {
			d := vels[j].Scale(dir)
			boxes[j] = moveBox(boxes[j], d)
			bp.Move(j, boxes[j], d)
		}
		if pairs := bp.UpdatePairs(); len(pairs) != 12 {
			b.Fatalf("got %d pairs, want 12", len(pairs))
		}
	}
}

// 对照：O(N^2) 两两暴力比对。
func BenchmarkBruteForce500(b *testing.B) {
	bp := New(0.1, 2.0)
	boxes, _ := buildAcceptanceScene(bp)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		for a := 0; a < len(boxes); a++ {
			for c := a + 1; c < len(boxes); c++ {
				if boxes[a].Overlaps(boxes[c]) {
					count++
				}
			}
		}
		if count != 12 {
			b.Fatalf("got %d pairs, want 12", count)
		}
	}
}
