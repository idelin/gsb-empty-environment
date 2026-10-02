// 运行样例：构建 1000 三角形场景，发射 100 条射线，
// 展示 BVH 剪枝效率与最近命中结果，并演示动态插入/删除。
package main

import (
	"fmt"
	"math/rand"
	"time"

	"bvh"
)

func main() {
	rng := rand.New(rand.NewSource(42))

	// 1. 构建 1000 个三角形的场景（10x10x10 空间簇）
	objs := make([]bvh.Object, 0, 1000)
	for i := 0; i < 1000; i++ {
		center := bvh.Vec3{
			X: float64(i%10)*8 + (rng.Float64()-0.5)*3,
			Y: float64((i/10)%10)*8 + (rng.Float64()-0.5)*3,
			Z: float64(i/100)*8 + (rng.Float64()-0.5)*3,
		}
		rv := func() bvh.Vec3 {
			return bvh.Vec3{X: rng.Float64()*2 - 1, Y: rng.Float64()*2 - 1, Z: rng.Float64()*2 - 1}
		}
		objs = append(objs, &bvh.Triangle{
			V0: center.Add(rv()), V1: center.Add(rv()), V2: center.Add(rv()),
		})
	}

	start := time.Now()
	tree := bvh.Build(objs)
	fmt.Printf("SAH 构建: %d 个三角形, 耗时 %v\n", tree.LeafCount(), time.Since(start))

	// 2. 发射 100 条射线
	rays := make([]bvh.Ray, 100)
	for i := range rays {
		tri := objs[rng.Intn(len(objs))].(*bvh.Triangle)
		target := tri.V0.Add(tri.V1).Add(tri.V2).Scale(1.0 / 3.0)
		origin := target.Add(bvh.Vec3{
			X: (rng.Float64() - 0.5) * 400,
			Y: (rng.Float64() - 0.5) * 400,
			Z: 200 + rng.Float64()*200,
		})
		rays[i] = bvh.Ray{Origin: origin, Dir: target.Sub(origin).Normalize()}
	}

	start = time.Now()
	hits, totalLeaves := 0, 0
	for _, r := range rays {
		_, _, ok, stats := tree.RayCastWithStats(r)
		if ok {
			hits++
		}
		totalLeaves += stats.LeavesVisited
	}
	elapsed := time.Since(start)
	avg := float64(totalLeaves) / float64(len(rays))
	fmt.Printf("射线查询: %d 条射线, %d 次命中, 总耗时 %v (%.3f ms/条)\n",
		len(rays), hits, elapsed, float64(elapsed.Microseconds())/1000/float64(len(rays)))
	fmt.Printf("剪枝效率: 平均访问叶子 %.1f/%d, 跳过 %.2f%% 的叶子节点\n",
		avg, tree.LeafCount(), (1-avg/float64(tree.LeafCount()))*100)

	// 3. 动态插入 / 删除
	moving := &bvh.Triangle{
		V0: bvh.Vec3{X: 0, Y: 0, Z: -100},
		V1: bvh.Vec3{X: 10, Y: 0, Z: -100},
		V2: bvh.Vec3{X: 0, Y: 10, Z: -100},
	}
	tree.Insert(moving)
	fmt.Printf("动态插入后叶子数: %d (树有效: %v)\n", tree.LeafCount(), tree.Validate())

	ray := bvh.Ray{Origin: bvh.Vec3{X: 2, Y: 2, Z: 0}, Dir: bvh.Vec3{X: 0, Y: 0, Z: -1}}
	if obj, t, ok := tree.RayCast(ray); ok {
		fmt.Printf("命中新插入物体: t=%.2f, 是新物体: %v\n", t, obj == moving)
	}

	tree.Remove(moving)
	fmt.Printf("动态删除后叶子数: %d (树有效: %v)\n", tree.LeafCount(), tree.Validate())
	if _, _, ok := tree.RayCast(ray); !ok {
		fmt.Println("删除后同一条射线不再命中（符合预期）")
	}
}
