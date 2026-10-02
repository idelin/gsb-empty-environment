// demo 是粗测碰撞筛选器的运行样例：
// 500 个刚体同时运动，每帧增量更新动态 AABB 树并筛出候选碰撞对。
package main

import (
	"fmt"
	"math"
	"time"

	"broadphase"
)

const (
	bodies   = 500
	wantPair = 12
	steps    = 200
)

func main() {
	bp := broadphase.NewBroadphase(0.5)
	half := broadphase.Vec3{X: 0.5, Y: 0.5, Z: 0.5}
	boxes := make(map[broadphase.EntityID]broadphase.AABB, bodies)

	// 场景：前 24 个刚体两两一组形成 12 对重叠，其余间隔排开。
	for i := 0; i < bodies; i++ {
		var c broadphase.Vec3
		if i < 2*wantPair {
			c = broadphase.Vec3{X: float64(i/2) * 10, Y: 100}
		} else {
			c = broadphase.Vec3{X: float64(i) * 10}
		}
		box := broadphase.NewAABB(c, half)
		boxes[broadphase.EntityID(i)] = box
		bp.Add(broadphase.EntityID(i), box)
	}
	fmt.Printf("scene: %d rigid bodies, expect %d overlapping pairs\n", bp.Count(), wantPair)

	start := time.Now()
	var lastPairs []broadphase.Pair
	for s := 0; s < steps; s++ {
		for id, box := range boxes {
			dx := 0.05
			if int(id) >= 2*wantPair {
				dx += 0.02 * math.Sin(float64(s)*0.3+float64(id))
			}
			box.Min.X += dx
			box.Max.X += dx
			boxes[id] = box
			bp.Move(id, box)
		}
		lastPairs = bp.Pairs()
	}
	elapsed := time.Since(start)

	fmt.Printf("broadphase: %d steps x (%d moves + pair query)\n", steps, bodies)
	fmt.Printf("total %v, avg %v/step\n", elapsed, elapsed/steps)
	fmt.Printf("candidate pairs for narrowphase: %d\n", len(lastPairs))
	for _, p := range lastPairs {
		fmt.Printf("  pair (%d, %d)\n", p.A, p.B)
	}
}
