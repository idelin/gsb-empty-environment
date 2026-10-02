package bvh

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

const (
	sceneTriangles = 1000
	sceneRays      = 100
)

// makeScene 生成 1000 个三角形：10x10x10 空间簇，每簇一个随机朝向三角形，
// 簇间留有空隙，形成适合 BVH 剪枝的分布结构。
func makeScene(seed int64) []Object {
	rng := rand.New(rand.NewSource(seed))
	objs := make([]Object, 0, sceneTriangles)
	for i := 0; i < sceneTriangles; i++ {
		// 簇中心分布在间距 8 的网格上
		cx := float64(i%10) * 8
		cy := float64((i/10)%10) * 8
		cz := float64(i/100) * 8
		jitter := func() float64 { return (rng.Float64() - 0.5) * 3 }
		center := Vec3{cx + jitter(), cy + jitter(), cz + jitter()}
		randV := func() Vec3 {
			return Vec3{rng.Float64()*2 - 1, rng.Float64()*2 - 1, rng.Float64()*2 - 1}
		}
		tri := &Triangle{
			V0: center.Add(randV()),
			V1: center.Add(randV()),
			V2: center.Add(randV()),
		}
		objs = append(objs, tri)
	}
	return objs
}

// makeRays 生成 100 条射线：大部分瞄准场景中的随机簇（保证有命中），
// 少部分随机方向（测试未命中路径）。
func makeRays(objs []Object, seed int64) []Ray {
	rng := rand.New(rand.NewSource(seed))
	rays := make([]Ray, 0, sceneRays)
	for i := 0; i < sceneRays; i++ {
		var target Vec3
		if i%5 != 4 {
			tri := objs[rng.Intn(len(objs))].(*Triangle)
			target = tri.V0.Add(tri.V1).Add(tri.V2).Scale(1.0 / 3.0)
		} else {
			target = Vec3{rng.Float64() * 200, rng.Float64() * 200, rng.Float64() * 200}
		}
		origin := target.Add(Vec3{
			(rng.Float64() - 0.5) * 400,
			(rng.Float64() - 0.5) * 400,
			200 + rng.Float64()*200,
		})
		rays = append(rays, Ray{Origin: origin, Dir: target.Sub(origin).Normalize()})
	}
	return rays
}

// bruteForce 暴力最近命中，作为正确性基准。
func bruteForce(objs []Object, r Ray) (Object, float64, bool) {
	best := math.Inf(1)
	var bestObj Object
	for _, o := range objs {
		if t, hit := o.Intersect(r); hit && t < best {
			best = t
			bestObj = o
		}
	}
	return bestObj, best, bestObj != nil
}

// TestBuildAndValidate 验证 SAH 构建的树结构完整。
func TestBuildAndValidate(t *testing.T) {
	objs := makeScene(42)
	tree := Build(objs)
	if tree.LeafCount() != sceneTriangles {
		t.Fatalf("LeafCount = %d, want %d", tree.LeafCount(), sceneTriangles)
	}
	if !tree.Validate() {
		t.Fatal("tree validation failed after SAH build")
	}
}

// TestRayCastCorrectnessAndPruning 验收核心：
// 1000 三角形场景 + 100 条射线，BVH 结果必须与暴力法一致，
// 且平均跳过 95% 以上的叶子节点，毫秒级返回。
func TestRayCastCorrectnessAndPruning(t *testing.T) {
	objs := makeScene(42)
	rays := makeRays(objs, 7)

	tree := Build(objs)

	start := time.Now()
	totalLeaves := 0
	hits := 0
	for _, r := range rays {
		obj, hitT, ok, stats := tree.RayCastWithStats(r)
		totalLeaves += stats.LeavesVisited

		wantObj, wantT, wantOk := bruteForce(objs, r)
		if ok != wantOk {
			t.Fatalf("hit mismatch: bvh=%v brute=%v (ray %+v)", ok, wantOk, r)
		}
		if !ok {
			continue
		}
		hits++
		if obj != wantObj {
			t.Fatalf("object mismatch: bvh t=%v, brute t=%v", hitT, wantT)
		}
		if math.Abs(hitT-wantT) > 1e-6*math.Max(1, wantT) {
			t.Fatalf("t mismatch: bvh=%v brute=%v", hitT, wantT)
		}
	}
	elapsed := time.Since(start)

	avgLeaves := float64(totalLeaves) / float64(len(rays))
	skipped := 1 - avgLeaves/float64(sceneTriangles)
	t.Logf("rays=%d hits=%d avg leaves visited=%.1f/%d (skipped %.2f%%), total time=%v (%.3f ms/ray)",
		len(rays), hits, avgLeaves, sceneTriangles, skipped*100, elapsed,
		float64(elapsed.Microseconds())/1000/float64(len(rays)))

	if skipped < 0.95 {
		t.Fatalf("pruning insufficient: skipped %.2f%% of leaves, want >= 95%%", skipped*100)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("too slow: %v for %d rays", elapsed, len(rays))
	}
}

// TestDynamicInsertRemove 验证动态插入/删除后树结构仍有效、查询仍正确。
func TestDynamicInsertRemove(t *testing.T) {
	objs := makeScene(123)
	tree := New()

	// 逐个动态插入
	for _, o := range objs {
		tree.Insert(o)
	}
	if tree.LeafCount() != sceneTriangles {
		t.Fatalf("after insert LeafCount = %d", tree.LeafCount())
	}
	if !tree.Validate() {
		t.Fatal("validation failed after dynamic inserts")
	}

	// 动态树查询结果应与暴力法一致
	rays := makeRays(objs, 9)
	for _, r := range rays[:20] {
		obj, _, ok := tree.RayCast(r)
		wantObj, _, wantOk := bruteForce(objs, r)
		if ok != wantOk || obj != wantObj {
			t.Fatal("dynamic tree raycast mismatch after inserts")
		}
	}

	// 删除一半物体
	remaining := make([]Object, 0, sceneTriangles/2)
	for i, o := range objs {
		if i%2 == 0 {
			if !tree.Remove(o) {
				t.Fatalf("Remove returned false for existing object %d", i)
			}
		} else {
			remaining = append(remaining, o)
		}
	}
	if tree.LeafCount() != len(remaining) {
		t.Fatalf("after remove LeafCount = %d, want %d", tree.LeafCount(), len(remaining))
	}
	if !tree.Validate() {
		t.Fatal("validation failed after removes")
	}

	// 删除后查询与暴力法一致，且已删除物体不会被命中
	for _, r := range rays {
		obj, _, ok := tree.RayCast(r)
		wantObj, _, wantOk := bruteForce(remaining, r)
		if ok != wantOk || obj != wantObj {
			t.Fatal("dynamic tree raycast mismatch after removes")
		}
	}

	// 删除不存在的物体返回 false
	if tree.Remove(objs[0]) {
		t.Fatal("Remove should return false for already-removed object")
	}

	// 再插入新物体仍正常
	extra := &Triangle{V0: Vec3{0, 0, -50}, V1: Vec3{4, 0, -50}, V2: Vec3{0, 4, -50}}
	tree.Insert(extra)
	if !tree.Validate() {
		t.Fatal("validation failed after re-insert")
	}
	ray := Ray{Origin: Vec3{1, 1, 0}, Dir: Vec3{0, 0, -1}}
	obj, _, ok := tree.RayCast(ray)
	if !ok || obj != extra {
		t.Fatal("expected to hit newly inserted triangle")
	}
}

// TestRayAABB slab 测试基本正确性。
func TestRayAABB(t *testing.T) {
	box := AABB{Min: Vec3{-1, -1, -1}, Max: Vec3{1, 1, 1}}
	if _, ok := box.IntersectRay(Ray{Vec3{0, 0, 5}, Vec3{0, 0, -1}}, math.Inf(1)); !ok {
		t.Fatal("expected hit")
	}
	if _, ok := box.IntersectRay(Ray{Vec3{5, 0, 5}, Vec3{0, 0, -1}}, math.Inf(1)); ok {
		t.Fatal("expected miss")
	}
	// maxT 提前剔除
	if _, ok := box.IntersectRay(Ray{Vec3{0, 0, 5}, Vec3{0, 0, -1}}, 2); ok {
		t.Fatal("expected miss due to maxT")
	}
}

// TestEmptyAndSingle 边界情况。
func TestEmptyAndSingle(t *testing.T) {
	tree := New()
	if _, _, ok := tree.RayCast(Ray{Vec3{0, 0, 0}, Vec3{1, 0, 0}}); ok {
		t.Fatal("empty tree should not hit")
	}
	tri := &Triangle{V0: Vec3{0, 0, 5}, V1: Vec3{1, 0, 5}, V2: Vec3{0, 1, 5}}
	tree.Insert(tri)
	if tree.LeafCount() != 1 || !tree.Validate() {
		t.Fatal("single insert invalid")
	}
	tree.Remove(tri)
	if tree.LeafCount() != 0 || !tree.Validate() {
		t.Fatal("remove last leaf invalid")
	}
}

// BenchmarkRayCast 性能基准。
func BenchmarkRayCast(b *testing.B) {
	objs := makeScene(42)
	rays := makeRays(objs, 7)
	tree := Build(objs)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.RayCast(rays[i%len(rays)])
	}
}
