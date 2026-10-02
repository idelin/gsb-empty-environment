package bvh

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// bruteForce 暴力遍历全部图元求最近命中，作为正确性基准。
func bruteForce(prims []Primitive, ray Ray) Hit {
	hit := Hit{T: math.Inf(1)}
	for _, p := range prims {
		if t, ok := p.Intersect(ray); ok && t < hit.T {
			hit.T = t
			hit.Prim = p
		}
	}
	return hit
}

// TestRaycastAcceptance 验收测试：
// 1000 个三角形建树，100 条射线求最近命中，
// 要求结果与暴力法一致、跳过 95% 以上叶子、毫秒级返回。
func TestRaycastAcceptance(t *testing.T) {
	prims, centers := makeScene(42, 20, 1000)
	rays := makeRays(7, 100, centers)

	tree := New()
	start := time.Now()
	tree.Build(prims)
	buildDur := time.Since(start)

	totalLeaves := tree.LeafCount()
	t.Logf("构建: %d 图元, %d 叶子, 耗时 %v", len(prims), totalLeaves, buildDur)

	var sumLeaves, sumPrims, sumNodes int
	hits := 0
	castStart := time.Now()
	for _, ray := range rays {
		got, st := tree.Raycast(ray)
		want := bruteForce(prims, ray)
		sumLeaves += st.LeavesVisited
		sumPrims += st.PrimTests
		sumNodes += st.NodesVisited

		if (got.Prim == nil) != (want.Prim == nil) {
			t.Fatalf("命中状态不一致: bvh=%v brute=%v ray=%+v",
				got.Prim != nil, want.Prim != nil, ray)
		}
		if got.Prim == nil {
			continue
		}
		hits++
		if math.Abs(got.T-want.T) > 1e-9*math.Max(1, want.T) {
			t.Fatalf("命中距离不一致: bvh=%v brute=%v", got.T, want.T)
		}
		if got.Prim != want.Prim {
			t.Fatalf("命中图元不一致: bvh=%v brute=%v", got.Prim, want.Prim)
		}
	}
	castDur := time.Since(castStart)

	avgLeaves := float64(sumLeaves) / float64(len(rays))
	avgPrims := float64(sumPrims) / float64(len(rays))
	skipRatio := 1 - avgLeaves/float64(totalLeaves)
	t.Logf("射线: %d 条, 命中 %d 条, 总耗时 %v (平均 %v/条)",
		len(rays), hits, castDur, castDur/time.Duration(len(rays)))
	t.Logf("剪枝: 平均访问叶子 %.1f/%d (跳过 %.2f%%), 平均精确测试 %.1f/%d 图元, 平均访问节点 %.1f",
		avgLeaves, totalLeaves, skipRatio*100, avgPrims, len(prims),
		float64(sumNodes)/float64(len(rays)))

	if skipRatio < 0.95 {
		t.Fatalf("剪枝率不足: 跳过 %.2f%% 叶子, 要求 >= 95%%", skipRatio*100)
	}
	if castDur > 100*time.Millisecond {
		t.Fatalf("射线求交过慢: 100 条耗时 %v, 要求毫秒级", castDur)
	}
}

// TestDynamicInsertRemove 验证动态插入/删除后查询结果仍与暴力法一致。
func TestDynamicInsertRemove(t *testing.T) {
	prims, centers := makeScene(1, 10, 500)
	rays := makeRays(2, 50, centers)

	tree := New()
	tree.Build(prims[:250])

	check := func(phase string, expect []Primitive) {
		t.Helper()
		if tree.Count() != len(expect) {
			t.Fatalf("%s: Count=%d, 期望 %d", phase, tree.Count(), len(expect))
		}
		for i, ray := range rays {
			got, _ := tree.Raycast(ray)
			want := bruteForce(expect, ray)
			if (got.Prim == nil) != (want.Prim == nil) {
				t.Fatalf("%s ray %d: 命中状态不一致", phase, i)
			}
			if got.Prim != nil && got.Prim != want.Prim {
				t.Fatalf("%s ray %d: 命中图元不一致 bvh.T=%v brute.T=%v",
					phase, i, got.T, want.T)
			}
		}
	}

	// 动态插入 250 个，再插入 500 个新图元。
	for _, p := range prims[250:500] {
		tree.Insert(p)
	}
	extra, _ := makeScene(99, 10, 500)
	for _, p := range extra {
		tree.Insert(p)
	}
	all := append(append([]Primitive{}, prims...), extra...)
	check("插入后", all)

	// 删除 60% 的图元。
	kept := make([]Primitive, 0, len(all))
	for i, p := range all {
		if i%5 < 2 {
			if !tree.Remove(p) {
				t.Fatalf("Remove 返回 false: 图元 %d", i)
			}
		} else {
			kept = append(kept, p)
		}
	}
	check("删除后", kept)

	// 重复删除应失败。
	if tree.Remove(all[0]) {
		t.Fatal("重复删除应返回 false")
	}

	// 全部删除后树应为空。
	for _, p := range kept {
		tree.Remove(p)
	}
	if tree.Count() != 0 {
		t.Fatalf("清空后 Count=%d", tree.Count())
	}
	if _, ok := tree.Bounds(); ok {
		t.Fatal("空树不应有包围盒")
	}
	if h, _ := tree.Raycast(rays[0]); h.Prim != nil {
		t.Fatal("空树不应有命中")
	}
}

// TestBoundsContainment 验证每个内部节点的包围盒都包住其子节点。
func TestBoundsContainment(t *testing.T) {
	prims, _ := makeScene(3, 8, 300)
	tree := New()
	tree.Build(prims)
	// 动态扰动后再验证。
	for i := 0; i < 100; i++ {
		tree.Remove(prims[i])
	}
	more, _ := makeScene(4, 4, 100)
	for _, p := range more {
		tree.Insert(p)
	}
	walk(tree.root, func(n *node) {
		if n.isLeaf() {
			for _, p := range n.prims {
				b := p.Bounds()
				if !n.bounds.Contains(b.Min) || !n.bounds.Contains(b.Max) {
					t.Fatal("叶子包围盒未包住图元")
				}
			}
			return
		}
		for _, c := range []*node{n.left, n.right} {
			if !n.bounds.Contains(c.bounds.Min) || !n.bounds.Contains(c.bounds.Max) {
				t.Fatal("内部节点包围盒未包住子节点")
			}
		}
	})
}

// disc 是二维图元（Z=0 平面内的圆盘），用于验证二维场景支持。
type disc struct {
	c Vec3
	r float64
}

func (d disc) Bounds() AABB {
	return AABB{
		Min: Vec3{d.c[0] - d.r, d.c[1] - d.r, 0},
		Max: Vec3{d.c[0] + d.r, d.c[1] + d.r, 0},
	}
}

func (d disc) Centroid() Vec3 { return d.c }

func (d disc) Intersect(ray Ray) (float64, bool) {
	// 二维射线-圆求交（在 Z=0 平面内）。
	oc := Vec3{ray.Orig[0] - d.c[0], ray.Orig[1] - d.c[1], 0}
	dir := Vec3{ray.Dir[0], ray.Dir[1], 0}
	a := dir.Dot(dir)
	if a < 1e-15 {
		return 0, false
	}
	b := 2 * oc.Dot(dir)
	c := oc.Dot(oc) - d.r*d.r
	delta := b*b - 4*a*c
	if delta < 0 {
		return 0, false
	}
	t := (-b - math.Sqrt(delta)) / (2 * a)
	if t < 1e-9 {
		return 0, false
	}
	return t, true
}

// Test2DScene 验证二维场景：Z=0 平面内的圆盘集合。
func Test2DScene(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	prims := make([]Primitive, 0, 400)
	for i := 0; i < 400; i++ {
		prims = append(prims, disc{
			c: Vec3{(rng.Float64()*2 - 1) * 100, (rng.Float64()*2 - 1) * 100, 0},
			r: 1 + rng.Float64()*3,
		})
	}
	tree := New()
	tree.Build(prims)

	for i := 0; i < 50; i++ {
		orig := Vec3{(rng.Float64()*2 - 1) * 150, (rng.Float64()*2 - 1) * 150, 0}
		target := Vec3{(rng.Float64()*2 - 1) * 100, (rng.Float64()*2 - 1) * 100, 0}
		ray := Ray{Orig: orig, Dir: target.Sub(orig).Normalize()}
		got, _ := tree.Raycast(ray)
		want := bruteForce(prims, ray)
		if (got.Prim == nil) != (want.Prim == nil) {
			t.Fatalf("ray %d: 命中状态不一致", i)
		}
		if got.Prim != nil && got.Prim != want.Prim {
			t.Fatalf("ray %d: 命中图元不一致", i)
		}
	}
}

// TestTriangleIntersect 单元测试：Möller–Trumbore 基本情形。
func TestTriangleIntersect(t *testing.T) {
	tri := Triangle{V0: Vec3{0, 0, 0}, V1: Vec3{1, 0, 0}, V2: Vec3{0, 1, 0}}
	ray := Ray{Orig: Vec3{0.25, 0.25, 1}, Dir: Vec3{0, 0, -1}}
	hitT, ok := tri.Intersect(ray)
	if !ok || math.Abs(hitT-1) > 1e-12 {
		t.Fatalf("正面命中失败: t=%v ok=%v", hitT, ok)
	}
	// 射线错过三角形。
	miss := Ray{Orig: Vec3{2, 2, 1}, Dir: Vec3{0, 0, -1}}
	if _, ok := tri.Intersect(miss); ok {
		t.Fatal("应未命中")
	}
	// 背后的三角形不应命中。
	behind := Ray{Orig: Vec3{0.25, 0.25, -1}, Dir: Vec3{0, 0, -1}}
	if _, ok := tri.Intersect(behind); ok {
		t.Fatal("背后三角形不应命中")
	}
}

// TestAABBHit 单元测试：slab 法射线-AABB。
func TestAABBHit(t *testing.T) {
	box := AABB{Min: Vec3{-1, -1, -1}, Max: Vec3{1, 1, 1}}
	if _, ok := box.hit(Ray{Orig: Vec3{0, 0, 5}, Dir: Vec3{0, 0, -1}}); !ok {
		t.Fatal("沿轴射线应命中")
	}
	if _, ok := box.hit(Ray{Orig: Vec3{0, 0, 5}, Dir: Vec3{0, 0, 1}}); ok {
		t.Fatal("背向射线不应命中")
	}
	if _, ok := box.hit(Ray{Orig: Vec3{5, 0, 0}, Dir: Vec3{0, 1, 0}}); ok {
		t.Fatal("平行偏离射线不应命中")
	}
	if tmin, ok := box.hit(Ray{Orig: Vec3{0, 0, 0}, Dir: Vec3{1, 0, 0}}); !ok || tmin != 0 {
		t.Fatalf("盒内原点应命中且 tmin=0, got tmin=%v ok=%v", tmin, ok)
	}
}
