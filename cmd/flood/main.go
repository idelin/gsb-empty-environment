// 运行样例：V 形谷地 DEM + 暴雨时序 -> 逐 tick 淹没演化、
// 连通区域边界多边形、道路通行断点列表。
package main

import (
	"fmt"
	"math"

	"floodsim"
)

func main() {
	const W, H = 12, 8

	// 1. 数字高程模型：中央纵向谷地 + 北高南低
	elev := make([]float64, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			d := math.Abs(float64(x) - float64(W-1)/2)
			elev[y*W+x] = d*1.5 + float64(H-1-y)*0.8
		}
	}
	// 渗透率：谷地土壤饱和（0.01），坡面 0.05 m/tick
	infil := make([]float64, W*H)
	for i := range infil {
		infil[i] = 0.08
	}
	for y := 0; y < H; y++ {
		infil[y*W+5], infil[y*W+6] = 0.02, 0.02
	}
	grid := floodsim.NewGrid(W, H, elev, infil)

	// 2. 暴雨时序：前 4 tick 强降雨，随后停雨
	rain := []float64{0.30, 0.40, 0.50, 0.40, 0.0, 0.0, 0.0, 0.0}
	sim := floodsim.NewSimulator(grid, rain)
	sim.FloodThreshold = 0.35

	// 3. 道路网：一条东西向主干道横穿谷地，一条沿谷道路
	net := &floodsim.RoadNetwork{Roads: []floodsim.Road{
		{Name: "main-st", Points: []floodsim.Point{{X: 0.5, Y: 3.5}, {X: 11.5, Y: 3.5}}},
		{Name: "valley-rd", Points: []floodsim.Point{{X: 5.5, Y: 0.5}, {X: 5.5, Y: 7.5}}},
	}}

	// 4. 逐步仿真并输出任意历史时刻状态
	sim.Run(len(rain))
	for t := 0; t <= sim.Tick(); t++ {
		regions := sim.RegionsAt(t)
		flooded := 0
		for _, r := range regions {
			flooded += len(r.Cells)
		}
		fmt.Printf("tick=%d water=%.2f m^2 flooded=%d cells regions=%d\n",
			t, sim.TotalWater(t), flooded, len(regions))
		for i, r := range regions {
			fmt.Printf("  region#%d cells=%d boundaryVertices=%d holes=%d first=%v\n",
				i, len(r.Cells), len(r.Boundary), len(r.Holes), r.Boundary[:4])
		}
		bps := sim.BreakpointsAt(t, net)
		for _, bp := range bps {
			fmt.Printf("  BREAK road=%s enter=(%.2f,%.2f) exit=(%.2f,%.2f)\n",
				bp.Road, bp.Enter.X, bp.Enter.Y, bp.Exit.X, bp.Exit.Y)
		}
	}
}
