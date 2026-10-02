// 运行样例：插入无人机巡航轨迹，查询 14:00–14:10、高度≥100m、
// 穿越核心保护区的所有轨迹段。
package main

import (
	"fmt"
	"math"
	"time"

	"stbvh"
)

var cst = time.FixedZone("CST", 8*3600)

func ts(hour, min, sec int) float64 {
	return float64(time.Date(2026, 10, 2, hour, min, sec, 0, cst).Unix())
}

func fmtT(t float64) string {
	return time.Unix(int64(t), 0).In(cst).Format("15:04:05")
}

func pt(x, y, z, t float64) stbvh.Point4 { return stbvh.Point4{x, y, z, t} }

func main() {
	// ---- 插入无人机巡航轨迹（X/Y 单位: 米, Z: 高度米, T: 时间）----
	trajs := []stbvh.Trajectory{
		{ID: 101, Points: []stbvh.Point4{ // 300m 高度横穿核心区
			pt(-500, 500, 300, ts(14, 2, 0)),
			pt(500, 500, 320, ts(14, 5, 0)),
			pt(1500, 500, 300, ts(14, 8, 0)),
		}},
		{ID: 102, Points: []stbvh.Point4{ // 同区域但高度仅 50m
			pt(-500, 500, 50, ts(14, 2, 0)),
			pt(1500, 500, 50, ts(14, 8, 0)),
		}},
		{ID: 103, Points: []stbvh.Point4{ // 高空但在保护区外
			pt(-500, 3000, 500, ts(14, 2, 0)),
			pt(1500, 3000, 500, ts(14, 8, 0)),
		}},
		{ID: 104, Points: []stbvh.Point4{ // 穿越核心区但时间不符
			pt(-500, 500, 200, ts(13, 50, 0)),
			pt(1500, 500, 200, ts(13, 55, 0)),
		}},
		{ID: 105, Points: []stbvh.Point4{ // 14:00 前后进入并爬升穿越
			pt(-2000, 500, 50, ts(13, 55, 0)),
			pt(500, 500, 400, ts(14, 5, 0)),
			pt(2000, 500, 600, ts(14, 12, 0)),
		}},
	}

	start := time.Now()
	bvh := stbvh.BuildTrajectories(trajs...)
	fmt.Printf("构建索引: %d 条轨迹, %d 个线段, 耗时 %v\n\n",
		len(trajs), bvh.Len(), time.Since(start))

	// ---- 查询: 14:00–14:10, 高度 ≥ 100m, 核心保护区 x,y ∈ [0,1000] ----
	query := stbvh.Box4{
		Min: [stbvh.NDim]float64{0, 0, 100, ts(14, 0, 0)},
		Max: [stbvh.NDim]float64{1000, 1000, math.Inf(1), ts(14, 10, 0)},
	}
	start = time.Now()
	hits := bvh.Query(query)
	elapsed := time.Since(start)

	fmt.Printf("查询: 14:00–14:10, 高度>=100m, 核心区 [0,1000]x[0,1000]\n")
	fmt.Printf("命中 %d 个轨迹段, 耗时 %v\n\n", len(hits), elapsed)
	for _, h := range hits {
		s := h.Segment
		fmt.Printf("无人机 #%d 段#%d [%s → %s]\n", s.TrajID, s.Index,
			fmtT(s.P0[stbvh.DimT]), fmtT(s.P1[stbvh.DimT]))
		fmt.Printf("  窗口内片段: (%.0f, %.0f, %.0f)@%s → (%.0f, %.0f, %.0f)@%s\n",
			h.Clip0[0], h.Clip0[1], h.Clip0[2], fmtT(h.Clip0[stbvh.DimT]),
			h.Clip1[0], h.Clip1[1], h.Clip1[2], fmtT(h.Clip1[stbvh.DimT]))
	}
}
