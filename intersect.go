package spacetime

import (
	"math"
	"sort"
)

// Interval 表示一段连续的时间区间 [Start, End](秒)。
type Interval struct {
	Start float64
	End   float64
}

// Duration 返回区间长度(秒)。
func (iv Interval) Duration() float64 { return iv.End - iv.Start }

// mergeEps 是相邻区间合并的时间容差(秒)。
const mergeEps = 1e-6

// IntersectIntervals 精确计算两轨迹距离小于 threshold 的全部
// 时间区间。ranges 为索引给出的候选时间窗;传 nil 表示在两条
// 轨迹的整个时间重叠范围内计算(即暴力基准)。
//
// 原理:取两轨迹采样时刻的并集作为分段点,每一段内两条轨迹
// 都是匀速直线运动,距离平方是关于时间的二次函数,解析求根
// 即可得到精确的进入/离开时刻,无任何采样遗漏。
func IntersectIntervals(a, b *Trajectory, threshold float64, ranges [][2]float64) []Interval {
	var out []Interval
	aStart, aEnd, okA := a.TimeRange()
	bStart, bEnd, okB := b.TimeRange()
	if !okA || !okB {
		return nil
	}
	lo := math.Max(aStart, bStart)
	hi := math.Min(aEnd, bEnd)
	if hi <= lo {
		return nil
	}
	if ranges == nil {
		ranges = [][2]float64{{lo, hi}}
	}
	r2 := threshold * threshold
	for _, rg := range ranges {
		start := math.Max(rg[0], lo)
		end := math.Min(rg[1], hi)
		if end <= start {
			continue
		}
		for _, iv := range intersectWindow(a, b, start, end, r2) {
			out = appendMerged(out, iv)
		}
	}
	return out
}

// intersectWindow 在 [start, end] 内逐段解析求交。
func intersectWindow(a, b *Trajectory, start, end, r2 float64) []Interval {
	points := []float64{start, end}
	for _, s := range a.Samples {
		if s.T > start && s.T < end {
			points = append(points, s.T)
		}
	}
	for _, s := range b.Samples {
		if s.T > start && s.T < end {
			points = append(points, s.T)
		}
	}
	sort.Float64s(points)

	var out []Interval
	for i := 0; i+1 < len(points); i++ {
		t0, t1 := points[i], points[i+1]
		if t1-t0 < 1e-12 {
			continue
		}
		ax0, ay0, _ := a.PositionAt(t0)
		ax1, ay1, _ := a.PositionAt(t1)
		bx0, by0, _ := b.PositionAt(t0)
		bx1, by1, _ := b.PositionAt(t1)
		for _, sub := range belowThreshold(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1, r2) {
			iv := Interval{
				Start: t0 + sub[0]*(t1-t0),
				End:   t0 + sub[1]*(t1-t0),
			}
			out = appendMerged(out, iv)
		}
	}
	return out
}

// belowThreshold 返回归一化参数 s∈[0,1] 上距离平方小于 r2
// 的子区间。两端轨迹段均按 s 线性插值。
func belowThreshold(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1, r2 float64) [][2]float64 {
	d0x, d0y := ax0-bx0, ay0-by0
	d1x, d1y := ax1-bx1, ay1-by1
	ddx, ddy := d1x-d0x, d1y-d0y

	// f(s) = A s^2 + B s + C,即 |d(s)|^2 - r^2。
	A := ddx*ddx + ddy*ddy
	B := 2 * (d0x*ddx + d0y*ddy)
	C := d0x*d0x + d0y*d0y - r2

	const eps = 1e-12
	if A < eps { // 近似相对静止
		if C < 0 {
			return [][2]float64{{0, 1}}
		}
		return nil
	}
	disc := B*B - 4*A*C
	if disc <= 0 {
		return nil
	}
	sq := math.Sqrt(disc)
	r1 := (-B - sq) / (2 * A)
	r2r := (-B + sq) / (2 * A)
	lo := math.Max(r1, 0)
	hi := math.Min(r2r, 1)
	if hi <= lo {
		return nil
	}
	return [][2]float64{{lo, hi}}
}

// appendMerged 把区间追加到有序结果中,相邻(间隙小于容差)
// 的区间被合并,保证跨分段点的相交区间连续完整。
func appendMerged(out []Interval, iv Interval) []Interval {
	if n := len(out); n > 0 && iv.Start-out[n-1].End < mergeEps {
		if iv.End > out[n-1].End {
			out[n-1].End = iv.End
		}
		return out
	}
	return append(out, iv)
}
