package broadphase

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

const (
	sceneBodies = 500
	scenePairs  = 12
	boxHalf     = 0.5
	margin      = 0.5
)

// buildScene 构造验收场景：500 个刚体，其中 24 个两两靠近形成 12 对重叠，
// 其余刚体间隔 10 个单位排开，互不重叠。
func buildScene() (*Broadphase, map[EntityID]AABB) {
	bp := NewBroadphase(margin)
	boxes := make(map[EntityID]AABB, sceneBodies)
	half := Vec3{boxHalf, boxHalf, boxHalf}

	for i := 0; i < sceneBodies; i++ {
		var center Vec3
		if i < 2*scenePairs {
			// 前 24 个刚体两两一组，组内重叠、组间远离。
			k := i / 2
			center = Vec3{float64(k) * 10, 100, 0}
		} else {
			center = Vec3{float64(i) * 10, 0, 0}
		}
		box := NewAABB(center, half)
		boxes[EntityID(i)] = box
		bp.Add(EntityID(i), box)
	}
	return bp, boxes
}

// bruteForcePairs 是 O(N^2) 暴力参照实现，用于校验粗测结果完全一致。
func bruteForcePairs(boxes map[EntityID]AABB) []Pair {
	ids := make([]int, 0, len(boxes))
	for id := range boxes {
		ids = append(ids, int(id))
	}
	var pairs []Pair
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := EntityID(ids[i]), EntityID(ids[j])
			if a > b {
				a, b = b, a
			}
			if boxes[a].Overlaps(boxes[b]) {
				pairs = append(pairs, Pair{A: a, B: b})
			}
		}
	}
	return pairs
}

func pairSet(pairs []Pair) map[Pair]bool {
	set := make(map[Pair]bool, len(pairs))
	for _, p := range pairs {
		set[p] = true
	}
	return set
}

// TestScene500Bodies 验收：500 个刚体中精确筛出 12 对重叠候选对，且与暴力法一致。
func TestScene500Bodies(t *testing.T) {
	bp, boxes := buildScene()
	if bp.Count() != sceneBodies {
		t.Fatalf("Count() = %d, want %d", bp.Count(), sceneBodies)
	}

	pairs := bp.Pairs()
	if len(pairs) != scenePairs {
		t.Fatalf("len(Pairs()) = %d, want %d: %v", len(pairs), scenePairs, pairs)
	}

	got := pairSet(pairs)
	want := pairSet(bruteForcePairs(boxes))
	if len(got) != len(want) {
		t.Fatalf("got %d pairs, brute force says %d", len(got), len(want))
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing pair %v", p)
		}
	}
	for p := range got {
		if !want[p] {
			t.Errorf("unexpected pair %v", p)
		}
		if p.A >= p.B {
			t.Errorf("pair %v not normalized (A < B)", p)
		}
	}
}

// TestMoveUpdate 验证增量移动：移走一对、制造新的一对、再还原。
func TestMoveUpdate(t *testing.T) {
	bp, boxes := buildScene()
	half := Vec3{boxHalf, boxHalf, boxHalf}

	// 把 1 号刚体移走，12 对变 11 对。
	boxes[1] = NewAABB(Vec3{0, 500, 0}, half)
	bp.Move(1, boxes[1])
	if n := len(bp.Pairs()); n != 11 {
		t.Fatalf("after moving #1 away: %d pairs, want 11", n)
	}

	// 把 24 号刚体挪到 26 号刚体位置，新增一对 (24,26)。
	boxes[24] = boxes[26]
	bp.Move(24, boxes[24])
	got := pairSet(bp.Pairs())
	if len(got) != 12 || !got[Pair{A: 24, B: 26}] {
		t.Fatalf("after moving #24 onto #26: %v, want 12 pairs incl (24,26)", got)
	}

	// 全部还原后与暴力法重新对齐。
	boxes[1] = NewAABB(Vec3{0, 100, 0}, half)
	bp.Move(1, boxes[1])
	boxes[24] = NewAABB(Vec3{240, 0, 0}, half)
	bp.Move(24, boxes[24])
	got = pairSet(bp.Pairs())
	want := pairSet(bruteForcePairs(boxes))
	if len(got) != len(want) {
		t.Fatalf("after restore: got %d pairs, brute force says %d", len(got), len(want))
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing pair %v after restore", p)
		}
	}
}

// TestSmallMovesStayIncremental 验证小幅移动不触发树重构，结果仍正确。
func TestSmallMovesStayIncremental(t *testing.T) {
	bp, boxes := buildScene()

	restructured := 0
	for step := 0; step < 5; step++ {
		for id, box := range boxes {
			shift := Vec3{0.05, 0, 0}
			box.Min.X += shift.X
			box.Max.X += shift.X
			boxes[id] = box
			if bp.Move(id, box) {
				restructured++
			}
		}
	}
	if restructured != 0 {
		t.Fatalf("%d tree reinserts for sub-margin moves, want 0", restructured)
	}
	if n := len(bp.Pairs()); n != scenePairs {
		t.Fatalf("after small moves: %d pairs, want %d", n, scenePairs)
	}
}

// TestRemove 验证移除实体后候选对同步消失。
func TestRemove(t *testing.T) {
	bp, _ := buildScene()
	bp.Remove(0)
	bp.Remove(3)
	if bp.Count() != sceneBodies-2 {
		t.Fatalf("Count() = %d, want %d", bp.Count(), sceneBodies-2)
	}
	pairs := bp.Pairs()
	if len(pairs) != scenePairs-2 {
		t.Fatalf("after removing 2 paired bodies: %d pairs, want %d", len(pairs), scenePairs-2)
	}
	for _, p := range pairs {
		if p.A == 0 || p.B == 0 || p.A == 3 || p.B == 3 {
			t.Errorf("pair %v references removed entity", p)
		}
	}
}

// TestRandomizedAgainstBruteForce 随机增删移，每步都与暴力法对照。
func TestRandomizedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	bp := NewBroadphase(margin)
	boxes := make(map[EntityID]AABB)
	half := Vec3{boxHalf, boxHalf, boxHalf}
	nextID := 0

	randomBox := func() AABB {
		c := Vec3{rng.Float64() * 100, rng.Float64() * 100, rng.Float64() * 100}
		return NewAABB(c, half)
	}

	for step := 0; step < 300; step++ {
		switch rng.Intn(3) {
		case 0: // 添加
			id := EntityID(nextID)
			nextID++
			box := randomBox()
			boxes[id] = box
			bp.Add(id, box)
		case 1: // 移动
			for id := range boxes {
				box := randomBox()
				boxes[id] = box
				bp.Move(id, box)
				break
			}
		case 2: // 删除
			for id := range boxes {
				bp.Remove(id)
				delete(boxes, id)
				break
			}
		}

		got := pairSet(bp.Pairs())
		want := pairSet(bruteForcePairs(boxes))
		if len(got) != len(want) {
			t.Fatalf("step %d: got %d pairs, brute force says %d", step, len(got), len(want))
		}
		for p := range want {
			if !got[p] {
				t.Fatalf("step %d: missing pair %v", step, p)
			}
		}
		validateTree(t, bp.tree)
	}
}

// validateTree 校验树的结构不变量：父指针、高度、包围盒包含关系。
func validateTree(t *testing.T, tree *Tree) {
	t.Helper()
	if tree.root == nullNode {
		return
	}
	var walk func(id int32, parent int32) int32
	walk = func(id int32, parent int32) int32 {
		n := &tree.nodes[id]
		if n.parent != parent {
			t.Errorf("node %d parent = %d, want %d", id, n.parent, parent)
		}
		if n.isLeaf() {
			if n.height != 0 {
				t.Errorf("leaf %d height = %d, want 0", id, n.height)
			}
			return 0
		}
		lh := walk(n.left, id)
		rh := walk(n.right, id)
		wantH := 1 + maxI32(lh, rh)
		if n.height != wantH {
			t.Errorf("node %d height = %d, want %d", id, n.height, wantH)
		}
		if !n.aabb.Contains(tree.nodes[n.left].aabb) || !n.aabb.Contains(tree.nodes[n.right].aabb) {
			t.Errorf("node %d aabb does not contain children", id)
		}
		if diff := lh - rh; diff > 1 || diff < -1 {
			t.Errorf("node %d unbalanced: left h=%d right h=%d", id, lh, rh)
		}
		return wantH
	}
	walk(tree.root, nullNode)
}

// TestPerformance500Bodies 验收性能：500 个刚体同时运动，
// 每步“移动全部刚体 + 筛出候选对”应远低于 O(N^2) 暴力法（数十微秒级）。
func TestPerformance500Bodies(t *testing.T) {
	bp, boxes := buildScene()

	const steps = 200
	start := time.Now()
	for s := 0; s < steps; s++ {
		// 全体刚体一起漂移 + 非配对刚体附加正弦扰动，配对刚体保持同步。
		drift := 0.05
		for id, box := range boxes {
			dx := drift
			if int(id) >= 2*scenePairs {
				dx += 0.02 * math.Sin(float64(s)*0.3+float64(id))
			}
			box.Min.X += dx
			box.Max.X += dx
			boxes[id] = box
			bp.Move(id, box)
		}
		pairs := bp.Pairs()
		if len(pairs) != scenePairs {
			t.Fatalf("step %d: %d pairs, want %d", s, len(pairs), scenePairs)
		}
	}
	elapsed := time.Since(start)
	perStep := elapsed / steps

	// 同机 O(N^2) 暴力参照。
	bruteStart := time.Now()
	for s := 0; s < steps; s++ {
		_ = bruteForcePairs(boxes)
	}
	brutePerStep := time.Since(bruteStart) / steps

	speedup := float64(brutePerStep) / float64(perStep)
	t.Logf("500 bodies x %d steps: broadphase avg %v/step, brute force avg %v/step, speedup %.1fx",
		steps, perStep, brutePerStep, speedup)
	if speedup < 5 {
		t.Fatalf("broadphase speedup %.1fx < 5x over brute force", speedup)
	}
	if perStep > 50*time.Millisecond {
		t.Fatalf("avg %v/step is pathologically slow", perStep)
	}
}

// BenchmarkMove500 基准：500 刚体整帧移动（增量更新）。
func BenchmarkMove500(b *testing.B) {
	bp, boxes := buildScene()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for id, box := range boxes {
			box.Min.X += 0.05
			box.Max.X += 0.05
			boxes[id] = box
			bp.Move(id, box)
		}
	}
}

// BenchmarkPairs500 基准：500 刚体场景的树内自相交遍历。
func BenchmarkPairs500(b *testing.B) {
	bp, _ := buildScene()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bp.Pairs()
	}
}

// BenchmarkBruteForce500 基准：O(N^2) 暴力两两比对参照。
func BenchmarkBruteForce500(b *testing.B) {
	_, boxes := buildScene()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bruteForcePairs(boxes)
	}
}

// BenchmarkBroadphase500 基准：500 刚体场景的单步粗测（移动 + 配对查询）。
func BenchmarkBroadphase500(b *testing.B) {
	bp, boxes := buildScene()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for id, box := range boxes {
			box.Min.X += 0.05
			box.Max.X += 0.05
			boxes[id] = box
			bp.Move(id, box)
		}
		_ = bp.Pairs()
	}
}

// ExampleBroadphase 演示最小用法。
func ExampleBroadphase() {
	bp := NewBroadphase(0.5)
	bp.Add(1, NewAABB(Vec3{0, 0, 0}, Vec3{1, 1, 1}))
	bp.Add(2, NewAABB(Vec3{1.5, 0, 0}, Vec3{1, 1, 1}))
	bp.Add(3, NewAABB(Vec3{100, 0, 0}, Vec3{1, 1, 1}))
	fmt.Println(bp.Pairs())
	// Output: [{1 2}]
}
