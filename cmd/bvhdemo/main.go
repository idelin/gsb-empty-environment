// bvhdemo 演示 BVH 树的构建、射线求交与动态增删。
//
// 场景：20 个星团共 1000 个随机三角形，散布在 120^3 的空间中；
// 发射 100 条空间射线，输出最近命中与剪枝统计。
package main

import (
	"fmt"
	"math/rand"
	"time"

	"bvh"
)

func makeScene(seed int64, nClusters, nTris int) ([]bvh.Primitive, []bvh.Vec3) {
	rng := rand.New(rand.NewSource(seed))
	per := nTris / nClusters
	prims := make([]bvh.Primitive, 0, nTris)
	centers := make([]bvh.Vec3, 0, nClusters)
	for c := 0; c < nClusters; c++ {
		center := bvh.Vec3{
			(rng.Float64()*2 - 1) * 60,
			(rng.Float64()*2 - 1) * 60,
			(rng.Float64()*2 - 1) * 60,
		}
		centers = append(centers, center)
		for i := 0; i < per; i++ {
			v0 := bvh.Vec3{
				center[0] + (rng.Float64()*2-1)*4,
				center[1] + (rng.Float64()*2-1)*4,
				center[2] + (rng.Float64()*2-1)*4,
			}
			v1 := bvh.Vec3{v0[0] + rng.Float64()*2, v0[1] + rng.Float64()*2, v0[2] + rng.Float64()*2}
			v2 := bvh.Vec3{v0[0] + rng.Float64()*2, v0[1] + rng.Float64()*2, v0[2] + rng.Float64()*2}
			prims = append(prims, bvh.Triangle{V0: v0, V1: v1, V2: v2})
		}
	}
	return prims, centers
}

func main() {
	prims, centers := makeScene(42, 20, 1000)

	tree := bvh.New()
	start := time.Now()
	tree.Build(prims)
	fmt.Printf("[构建] %d 个三角形, SAH 建树耗时 %v, 叶子数 %d\n",
		len(prims), time.Since(start), tree.LeafCount())

	// 发射 100 条射线：2/3 瞄准星团，1/3 随机方向。
	rng := rand.New(rand.NewSource(7))
	const nRays = 100
	var sumLeaves, sumPrims int
	hits := 0
	castStart := time.Now()
	var firstHit bvh.Hit
	for i := 0; i < nRays; i++ {
		orig := bvh.Vec3{
			(rng.Float64()*2 - 1) * 90,
			(rng.Float64()*2 - 1) * 90,
			(rng.Float64()*2 - 1) * 90,
		}
		var target bvh.Vec3
		if i%3 != 0 {
			c := centers[rng.Intn(len(centers))]
			target = bvh.Vec3{
				c[0] + (rng.Float64()*2-1)*2,
				c[1] + (rng.Float64()*2-1)*2,
				c[2] + (rng.Float64()*2-1)*2,
			}
		} else {
			target = bvh.Vec3{
				(rng.Float64()*2 - 1) * 50,
				(rng.Float64()*2 - 1) * 50,
				(rng.Float64()*2 - 1) * 50,
			}
		}
		ray := bvh.Ray{Orig: orig, Dir: target.Sub(orig).Normalize()}
		hit, st := tree.Raycast(ray)
		sumLeaves += st.LeavesVisited
		sumPrims += st.PrimTests
		if hit.Prim != nil {
			hits++
			if firstHit.Prim == nil {
				firstHit = hit
			}
		}
	}
	castDur := time.Since(castStart)

	totalLeaves := tree.LeafCount()
	avgLeaves := float64(sumLeaves) / nRays
	fmt.Printf("[求交] %d 条射线, 命中 %d 条, 总耗时 %v (平均 %v/条)\n",
		nRays, hits, castDur, castDur/nRays)
	fmt.Printf("[剪枝] 平均访问叶子 %.1f/%d (跳过 %.2f%%), 平均精确三角形测试 %.1f/%d 次\n",
		avgLeaves, totalLeaves, (1-avgLeaves/float64(totalLeaves))*100,
		float64(sumPrims)/nRays, len(prims))
	if firstHit.Prim != nil {
		tri := firstHit.Prim.(bvh.Triangle)
		fmt.Printf("[样例] 最近命中: t=%.4f, 三角形 V0=%v\n", firstHit.T, tri.V0)
	}

	// 动态更新：删除 200 个、插入 200 个新三角形。
	for _, p := range prims[:200] {
		tree.Remove(p)
	}
	fresh, _ := makeScene(99, 4, 200)
	for _, p := range fresh {
		tree.Insert(p)
	}
	fmt.Printf("[动态] 删除 200 + 插入 200 后, 图元数 %d, 叶子数 %d\n",
		tree.Count(), tree.LeafCount())

	// 动态更新后再次求交验证可用性。
	h, _ := tree.Raycast(bvh.Ray{
		Orig: bvh.Vec3{0, 0, 90},
		Dir:  bvh.Vec3{0, 0, -1},
	})
	if h.Prim != nil {
		fmt.Printf("[动态] 更新后沿 -Z 射线命中 t=%.4f\n", h.T)
	} else {
		fmt.Println("[动态] 更新后沿 -Z 射线未命中")
	}
}
