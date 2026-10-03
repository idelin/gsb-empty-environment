// demo 模拟验收场景：500 个刚体同时运动 120 帧，
// 粗测模块每帧筛出 12 对包围盒重叠的候选对，并与 O(N^2) 暴力法对比耗时。
package main

import (
	"fmt"
	"math/rand"
	"time"

	"broadphase"
)

const (
	numBodies = 500
	numPairs  = 12
	frames    = 120
)

type body struct {
	box broadphase.AABB
	vel broadphase.Vec2
}

func boxAt(x, y float64) broadphase.AABB {
	return broadphase.AABB{
		Min: broadphase.Vec2{X: x - 0.5, Y: y - 0.5},
		Max: broadphase.Vec2{X: x + 0.5, Y: y + 0.5},
	}
}

func moveBox(b broadphase.AABB, d broadphase.Vec2) broadphase.AABB {
	return broadphase.AABB{
		Min: broadphase.Vec2{X: b.Min.X + d.X, Y: b.Min.Y + d.Y},
		Max: broadphase.Vec2{X: b.Max.X + d.X, Y: b.Max.Y + d.Y},
	}
}

func main() {
	rng := rand.New(rand.NewSource(7))
	bp := broadphase.New(0.1, 2.0) // 肥胖边距 0.1，碰撞预测系数 2.0

	bodies := make([]body, numBodies)
	// 网格散布，间距 10，互不重叠。
	const cols = 23
	for i := range bodies {
		x := float64(i%cols) * 10
		y := float64(i/cols) * 10
		bodies[i] = body{
			box: boxAt(x, y),
			vel: broadphase.Vec2{X: rng.Float64()*0.04 - 0.02, Y: rng.Float64()*0.04 - 0.02},
		}
	}
	// 埋入 12 对重叠刚体，彼此同速运动以保持重叠。
	for p := 0; p < numPairs; p++ {
		a, b := 2*p, 2*p+1
		base := broadphase.Vec2{X: -1000 + float64(p)*20, Y: -1000}
		v := broadphase.Vec2{X: 0.03, Y: -0.02}
		bodies[a].box, bodies[a].vel = boxAt(base.X, base.Y), v
		bodies[b].box, bodies[b].vel = boxAt(base.X+0.5, base.Y), v
	}
	for i := range bodies {
		bp.Add(i, bodies[i].box)
	}

	var broadNS, bruteNS int64
	var lastPairs []broadphase.Pair
	for f := 0; f < frames; f++ {
		// 1. 所有刚体同时运动，增量更新动态 AABB 树。
		start := time.Now()
		for i := range bodies {
			bodies[i].box = moveBox(bodies[i].box, bodies[i].vel)
			bp.Move(i, bodies[i].box, bodies[i].vel)
		}
		// 2. 树内自相交遍历，产出潜在碰撞对。
		lastPairs = bp.UpdatePairs()
		broadNS += time.Since(start).Nanoseconds()

		// 3. 对照：O(N^2) 两两暴力比对。
		start = time.Now()
		n := 0
		for a := 0; a < numBodies; a++ {
			for b := a + 1; b < numBodies; b++ {
				if bodies[a].box.Overlaps(bodies[b].box) {
					n++
				}
			}
		}
		bruteNS += time.Since(start).Nanoseconds()
		if n != numPairs {
			panic(fmt.Sprintf("frame %d: brute force found %d pairs, want %d", f, n, numPairs))
		}
		if len(lastPairs) != numPairs {
			panic(fmt.Sprintf("frame %d: broadphase found %d pairs, want %d", f, len(lastPairs), numPairs))
		}
	}

	fmt.Printf("场景: %d 个刚体同时运动, %d 帧\n", numBodies, frames)
	fmt.Printf("动态树高度: %d (log2(%d) ≈ 9)\n", bp.TreeHeight(), numBodies)
	fmt.Printf("每帧候选对: %d (已去重排序, 移交窄测)\n", len(lastPairs))
	fmt.Printf("候选对列表: %v\n", lastPairs)
	fmt.Printf("粗测耗时: 平均 %.1f µs/帧 (含 500 次移动更新 + 自相交遍历)\n",
		float64(broadNS)/float64(frames)/1000)
	fmt.Printf("暴力对照: 平均 %.1f µs/帧 (O(N^2) = %d 次两两比对)\n",
		float64(bruteNS)/float64(frames)/1000, numBodies*(numBodies-1)/2)
}
